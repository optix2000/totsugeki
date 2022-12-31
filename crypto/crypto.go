package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
)

// As of the December 2022 patch, requests/responses from the API are GCM encrypted
// See https://github.com/optix2000/totsugeki/issues/86

const ivLen = 12

// ContextDecryptedBodyKey used to access the decrypted body inside a request Context
const ContextDecryptedBodyKey = "DecryptedBodyKey"

var aesgcm cipher.AEAD

// init intializes the cipher struct for the other functions
func init() {
	// key obtained from hooking strive EVP_EncryptInit_ex_0
	// RVA: 0x3036460
	// Encoding + Concating RVA: 0xB248D0
	// GGST Timestamp: 63906742
	key, err := hex.DecodeString("EEBC1F57487F51921C0465665F8AE6D1658BB26DE6F8A069A3520293A572078F")
	if err != nil {
		panic(err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}

	aesgcm, err = cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
}

// Decrypt decrypts the GGST's api's responses
func Decrypt(encrypted []byte) ([]byte, error) {
	if len(encrypted) <= ivLen {
		return nil, fmt.Errorf("Decrypt encrypted []byte must longer than %d", ivLen)
	}

	iv := encrypted[:ivLen]
	plainText, err := aesgcm.Open(nil, iv, encrypted[ivLen:], nil)
	if err != nil {
		return nil, fmt.Errorf("Decrypt Open failure: %s", err)
	}
	fmt.Printf("Decrypt: %s\n", plainText)
	return plainText, nil
}

// Encrypt encrypts request bodies, to be sent to the GGST API.
func Encrypt(payload []byte) ([]byte, error) {
	iv := make([]byte, ivLen)
	_, err := rand.Read(iv)
	if err != nil {
		return nil, err
	}

	encrypted := aesgcm.Seal(nil, iv, payload, nil)

	out := append(iv, encrypted...)
	return out, nil
}

// Middleware decrypts incoming requests, and adds the decrypted body into the context
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encryptedBody, err := io.ReadAll(r.Body)
		if err != nil {
			fmt.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		decryptedBody, err := Decrypt(encryptedBody)
		if err != nil {
			fmt.Println("Decrypt failure", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// add the decrypted body into the request context
		ctx := context.WithValue(r.Context(), ContextDecryptedBodyKey, decryptedBody)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
