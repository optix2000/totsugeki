package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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
	AsyncStatsSet   bool
	PredictStatsGet bool
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

func CreateStriveProxy(listen string, GGStriveAPIURL string, PatchedAPIURL string, options *StriveAPIProxyOptions) *StriveAPIProxy {

	transport := http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        2,
		MaxIdleConnsPerHost: 1,
		MaxConnsPerHost:     2,
		IdleConnTimeout:     90 * time.Second, // Drop idle connection after 90 seconds to balance between being nice to ASW and keeping things fast.
	}
	client := http.Client{
		Transport: &transport,
	}

	proxy := &StriveAPIProxy{
		Client:         &client,
		Server:         &http.Server{Addr: listen},
		GGStriveAPIURL: GGStriveAPIURL,
		PatchedAPIURL:  PatchedAPIURL,
	}

	statsSet := proxy.HandleCatchall
	statsGet := proxy.HandleCatchall
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	if options.AsyncStatsSet {
		statsSet = proxy.HandleStatsSet
		proxy.statsQueue = proxy.startStatsSender()
	}
	if options.PredictStatsGet {
		predictStatsTransport := transport
		predictStatsTransport.MaxIdleConns = StatsGetWorkers
		predictStatsTransport.MaxIdleConnsPerHost = StatsGetWorkers
		predictStatsTransport.MaxConnsPerHost = StatsGetWorkers
		predictStatsTransport.IdleConnTimeout = 10 * time.Second // Quickly drop connections since this is a one-shot.
		predictStatsClient := client
		predictStatsClient.Transport = &predictStatsTransport

		prediction := CreateStatsGetPrediction(GGStriveAPIURL, &predictStatsClient)
		r.Use(prediction.StatsGetStateHandler)
		statsGet = func(w http.ResponseWriter, r *http.Request) {
			if !prediction.HandleGetStats(w, r) {
				proxy.HandleCatchall(w, r)
			}
		}
	}

	r.Route("/api", func(r chi.Router) {
		r.HandleFunc("/sys/get_env", proxy.HandleGetEnv)
		r.HandleFunc("/statistics/get", statsGet)
		r.HandleFunc("/statistics/set", statsSet)
		r.HandleFunc("/*", proxy.HandleCatchall)
	})

	proxy.Server.Handler = r
	return proxy
}
