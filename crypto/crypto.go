package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
)

// As of the December 2022 patch, requests/responses from the API are GCM encrypted
// See https://github.com/optix2000/totsugeki/issues/86

const ivLen = 12

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
		return nil, fmt.Errorf("encrypted []byte must longer than %d", ivLen)
	}

	iv := encrypted[:12]
	plainText, err := aesgcm.Open(nil, iv, encrypted[12:], nil)
	if err != nil {
		return nil, err
	}
	return plainText, nil
}

// Encrypt encrypts request bodies, to be sent to the GGST API.
func Encrypt(payload []byte) ([]byte, error) {
	iv := make([]byte, 12)
	_, err := rand.Read(iv)
	if err != nil {
		return nil, err
	}

	encrypted := aesgcm.Seal(nil, iv, payload, nil)

	out := append(iv, encrypted...)
	return out, nil
}

// Middleware decrypts incoming requests, and decrypts the outgoing request
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encryptedBody, err := ioutil.ReadAll(r.Body)
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

		// put the decrypted body back in the request body
		r.Body = ioutil.NopCloser(bytes.NewReader(decryptedBody))

		// catch the original writer and write the response as usual
		unencryptedWriter := encryptResponseWriter{
			ResponseWriter: w,
			buf:            &bytes.Buffer{},
		}
		next.ServeHTTP(unencryptedWriter, r)

		unencryptedBytes, err := ioutil.ReadAll(unencryptedWriter.buf)
		if err != nil {
			fmt.Println("read unencrypted response failure", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		encryptedResponse, err := Encrypt(unencryptedBytes)
		if err != nil {
			fmt.Println("Encrypt failure", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		_, err = io.Copy(w, bytes.NewReader(encryptedResponse))
		if err != nil {
			fmt.Println("rewriting response failure", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

	})
}

type encryptResponseWriter struct {
	http.ResponseWriter
	buf *bytes.Buffer
}
