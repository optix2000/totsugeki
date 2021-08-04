package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"golang.org/x/time/rate"
)

type StriveAPIProxy struct {
	Client         *http.Client
	Server         *http.Server
	GGStriveAPIURL string
	PatchedAPIURL  string
	statsQueue     chan<- *http.Request
	wg             sync.WaitGroup
}

type StriveAPIProxyOptions struct {
	AsyncStatsSet bool
}

func (s *StriveAPIProxy) proxyRequest(r *http.Request) (*http.Response, error) {
	apiURL, err := url.Parse(s.GGStriveAPIURL) // TODO: Const this
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	apiURL.Path = r.URL.Path

	r.URL = apiURL
	r.Host = ""
	r.RequestURI = ""
	return s.Client.Do(r)
}

// Proxy everything else
func (s *StriveAPIProxy) HandleCatchall(w http.ResponseWriter, r *http.Request) {
	resp, err := s.proxyRequest(r)
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	// Copy headers
	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	reader := io.TeeReader(resp.Body, w) // For dumping API payloads
	_, err = io.ReadAll(reader)
	if err != nil {
		fmt.Println(err)
	}
}

// GGST uses the URL from this API after initial launch so we need to intercept this.
func (s *StriveAPIProxy) HandleGetEnv(w http.ResponseWriter, r *http.Request) {
	resp, err := s.proxyRequest(r)
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	// Copy headers
	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err)
	}
	buf = bytes.Replace(buf, []byte(s.GGStriveAPIURL), []byte(s.PatchedAPIURL), -1)
	w.Write(buf)
}

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
		Version1  [5]byte  // Some sort of ASCII version number. "0.0.5", was "0.0.4" in v1.05
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
		Version1:  [5]byte{0x30, 0x2e, 0x30, 0x2e, 0x35}, // 0.0.5
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
				rl.Wait(context.Background())
				newReq := req.Clone(context.Background())
				res, err := s.proxyRequest(newReq) // TODO: Maybe capture result to fake hashes better.
				if err != nil {
					fmt.Println(err)
					continue
				}
				if res.StatusCode != http.StatusOK {
					fmt.Println(res)
					io.Copy(io.Discard, res.Body)
					res.Body.Close()
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

func (s *StriveAPIProxy) Shutdown() {
	fmt.Println("Shutting down proxy...")

	err := s.Server.Shutdown(context.Background())
	if err != nil {
		fmt.Println(err)
	}

	s.stopStatsSender()

	fmt.Println("Waiting for connections to complete...")
	s.wg.Wait()
}

func CreateStriveProxy(listen string, GGStriveAPIURL string, PatchedAPIURL string, options *StriveAPIProxyOptions) *http.Server {

	proxy := &StriveAPIProxy{
		Client: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
				ResponseHeaderTimeout: 1 * time.Minute, // Some people have _really_ slow internet to Japan.
				MaxIdleConns:          2,
				MaxIdleConnsPerHost:   1,
				MaxConnsPerHost:       2,
				IdleConnTimeout:       90 * time.Second, // Drop idle connection after 90 seconds to balance between being nice to ASW and keeping things fast.
				TLSHandshakeTimeout:   30 * time.Second,
			},
			Timeout: 3 * time.Minute, // 2x the slowest request I've seen.
		},
		Server:         &http.Server{Addr: listen},
		GGStriveAPIURL: GGStriveAPIURL,
		PatchedAPIURL:  PatchedAPIURL,
	}

	statsSet := proxy.HandleCatchall

	if options.AsyncStatsSet {
		statsSet = proxy.HandleStatsSet
		proxy.statsQueue = proxy.startStatsSender()
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Route("/api", func(r chi.Router) {
		r.HandleFunc("/sys/get_env", proxy.HandleGetEnv)
		r.HandleFunc("/statistics/set", statsSet)
		r.HandleFunc("/*", proxy.HandleCatchall)
	})

	proxy.Server.Handler = r
	return proxy.Server
}
