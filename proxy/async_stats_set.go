package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

// UNSAFE
// HandleStatsSet is the handler for buffered stats/set so the stats can be flushed asynchronously from the game.
func (s *StriveAPIProxy) HandleStatsSet(w http.ResponseWriter, r *http.Request) {
	req := r.Clone(context.Background())
	// https://github.com/golang/go/issues/36095
	var b bytes.Buffer
	b.ReadFrom(r.Body)
	req.Body = ioutil.NopCloser(bytes.NewReader(b.Bytes()))

	s.statsQueue <- req

	// Fake headers (v1.07)
	header := w.Header()

	header.Set("Content-Type", "text/html; charset=UTF-8") // GGST didn't care if this wasn't set, but why not
	header.Set("Server", "Apache")                         // GGST didn't care if this wasn't set, but why not
	header.Set("X-Powered-By", "PHP/7.2.34")               // GGST didn't care if this wasn't set, but why not

	// Fake body (v1.07)
	type statsSetBody struct { // 59 bytes
		Header    [4]byte  // Unknown use. Always 0x9298ad36
		Hash      [12]byte // Some sort of incrementing hash? Maybe a Req/Resp ID.
		Spacer1   [2]byte  // Unknown use. Always 0x00b3
		Timestamp [19]byte // Current time in "YYYY/MM/DD HH:MM:SS" in UTC
		Spacer2   [1]byte  // Unknown use. Always 0xa5
		Version1  [5]byte  // Some sort of ASCII version number. "0.1.1" in v1.16. "0.0.7" in v1.10. "0.0.6" in v1.07. "0.0.5" in v1.06, was "0.0.4" in v1.05
		Spacer3   [1]byte  // Unknown use. Always 0xa5
		Version2  [5]byte  // Another version number. Always "0.0.2"
		Spacer4   [1]byte  // Unknown use. Always 0xa5
		Version3  [5]byte  // ANOTHER version number. Always "0.0.2"
		Footer    [4]byte  // Unknown use. 0xa0a09100
	}

	var hash [12]byte // Does it even matter if we fill this?
	copy(hash[:], "badddeadc0de")

	var timestamp [19]byte
	copy(timestamp[:], time.Now().UTC().Format("2006/01/02 15:04:05"))

	body := statsSetBody{
		Header:    [4]byte{0x92, 0x98, 0xad, 0x36},
		Hash:      hash,
		Spacer1:   [2]byte{0x00, 0xb3},
		Timestamp: timestamp,
		Spacer2:   [1]byte{0xa5},
		Version1:  [5]byte{0x30, 0x2e, 0x31, 0x2e, 0x31}, // 0.1.1
		Spacer3:   [1]byte{0xa5},
		Version2:  [5]byte{0x30, 0x2e, 0x30, 0x2e, 0x32}, // 0.0.2
		Spacer4:   [1]byte{0xa5},
		Version3:  [5]byte{0x30, 0x2e, 0x30, 0x2e, 0x32}, // 0.0.2
		Footer:    [4]byte{0xa0, 0xa0, 0x91, 0x00},
	}

	err := binary.Write(w, binary.BigEndian, body)
	if err != nil {
		fmt.Println(err)
	}
}

func (s *StriveAPIProxy) startStatsSender() chan<- *http.Request {
	reqQueue := make(chan *http.Request, 32) // TODO: Don't hardcode size. Needs to have enough for a normal /api/stats/set burst.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		rl := rate.NewLimiter(rate.Every(100*time.Millisecond), 1) // GGST always waits 100ms between API calls.
		fmt.Println("Started /api/statistics/set sender.")
		for req := range reqQueue {
			// Retry the writes, since we're now responsible for them.
			// Loses transparency here as we don't may not react the same was as the client.
			for i := 0; i < 5; i++ { // GGST retries 5 times on stats/write
				newReq := req.Clone(context.Background())
				res, err := s.proxyRequest(newReq) // TODO: Maybe capture result to fake hashes better.
				if err != nil {
					fmt.Println(err)
					rl.Wait(context.Background())
					continue
				}
				if res.StatusCode != http.StatusOK {
					fmt.Println(res)
					io.Copy(io.Discard, res.Body)
					res.Body.Close()
					rl.Wait(context.Background())
					continue
				}
				fmt.Println("Asynchronously uploaded stats.")
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				break
			}
		}
	}()

	return reqQueue
}

func (s *StriveAPIProxy) stopStatsSender() {
	if s.statsQueue != nil {
		close(s.statsQueue)
	}
}
