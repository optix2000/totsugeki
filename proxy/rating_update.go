package proxy

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/optix2000/totsugeki/ggst"
	"github.com/vmihailenco/msgpack/v5"
)

const ratingUpdateURL = "http://ratingupdate.info/api/player_rating/%s"

// var characters = map[string]string{
// 	// "SOL": "SO",
// 	// "KYK": "KY",
// 	// "MAY": "MA",
// 	// "AXL": "AX",
// 	// "CHP": "CH",
// 	// "POT": "PO",
// 	// "FAU": "FA",
// 	"MLL": "MI",
// 	"ZAT": "ZT",
// 	// "RAM": "RA",
// 	// "LEO": "LE",
// 	// "NAG": "NA",
// 	// "GIO": "GI",
// 	// "ANJ": "AN",
// 	// "INO": "IN",
// 	"GLD": "GO",
// 	"JKO": "JC",
// }

var characters = map[string]int{
	"SOL": 0,
	"KYK": 1,
	"MAY": 2,
	"AXL": 3,
	"CHP": 4,
	"POT": 5,
	"FAU": 6,
	"MLL": 7,
	"ZAT": 8,
	"RAM": 9,
	"LEO": 10,
	"NAG": 11,
	"GIO": 12,
	"ANJ": 13,
	"INO": 14,
	"GLD": 15,
	"JKO": 16,
	"COS": 17,
	"BKN": 18,
	"TST": 19,
}

type RatingUpdate struct {
	client http.Client
}

func (ru *RatingUpdate) RatingUpdateHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/statistics/get" {
			ru.InjectRating(next, w, r)
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

func NewRatingUpdate() *RatingUpdate {
	return &RatingUpdate{
		client: http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    1,
				MaxConnsPerHost: 2,
				IdleConnTimeout: 30 * time.Second,
			},
		},
	}
}

func (ru *RatingUpdate) InjectRating(next http.Handler, w http.ResponseWriter, r *http.Request) {
	reqBody := &bytes.Buffer{}
	r.Body = io.NopCloser(io.TeeReader(r.Body, reqBody)) // Tee body so it can be read multiple times

	parsedReq := &ggst.StatGetRequest{}
	err := ggst.ParseReq(r, parsedReq)
	r.Body = io.NopCloser(reqBody) // Reset request body for next handler
	if err != nil ||
		parsedReq.Payload.Type != 7 || // We only care about injecting ratings into character levels
		parsedReq.Payload.OtherUserID == "" { // Abort if we are fetching our own rating. Injecting our own rating will break our R-Code.
		if err != nil {
			fmt.Println(err)
		}
		next.ServeHTTP(w, r)
		return
	}

	userID, err := strconv.ParseUint(parsedReq.Payload.OtherUserID, 10, 64)
	if err != nil {
		fmt.Println(err)
		next.ServeHTTP(w, r)
	}

	wg := sync.WaitGroup{}
	var ratings Ratings
	wg.Add(1)
	go func() {
		ratings, err = ru.FetchRatings(uint64(userID))
		wg.Done()
	}()

	ww := &ggst.BufferedResponseWriter{
		HttpHeader: http.Header{},
		StatusCode: 0,
		Body:       bytes.Buffer{},
	}

	next.ServeHTTP(ww, r)

	if err != nil {
		fmt.Println(err)
		w.Write(ww.Body.Bytes())
		return
	}

	for k, v := range ww.Header() { // Copy headers across
		if k == "Content-Length" {
			continue
		}
		w.Header()[k] = v
	}

	parsedResp, err := ggst.UnmarshalStatResp(ww.Body.Bytes())
	if err != nil {
		fmt.Println(err)
		w.Write(ww.Body.Bytes())
		return
	}

	wg.Wait() // Wait for fetchRatings to finish
	for k := range parsedResp.Payload.JSON {
		if strings.HasSuffix(k, "Lv") {

			if err != nil {
				fmt.Println(err)
				continue
			}
			idx := convertCharacter(k[0:3])
			if idx == -1 {
				fmt.Println("Unknown character:", k[0:3])
				continue
			}
			if len(ratings) <= idx {
				fmt.Println("No rating for:", k[0:3])
				continue
			}
			rating := ratings[idx]
			parsedResp.Payload.JSON[k] = int(math.Round(rating.Value))
		}
	}

	out, err := msgpack.Marshal(parsedResp)
	if err != nil {
		fmt.Println(err)
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(out)))
	w.Write(out)
}

type Ratings []Rating

type Rating struct {
	Value     float64
	Deviation float64
}

func (ru *RatingUpdate) FetchRatings(userID uint64) (Ratings, error) {
	url := fmt.Sprintf(ratingUpdateURL, convertUser(userID))
	resp, err := ru.client.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP error: %s. URL: %s", resp.Status, url)
	}
	d := json.NewDecoder(resp.Body)

	ratings := &Ratings{}
	err = d.Decode(ratings)
	if err != nil {
		return nil, err
	}

	return (*ratings), nil
}

func convertCharacter(character string) int {
	if val, ok := characters[character]; ok {
		return val
	} else {
		return -1
	}
}

func convertUser(userID uint64) string {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, userID)
	return strings.ToUpper(hex.EncodeToString(b))
}
