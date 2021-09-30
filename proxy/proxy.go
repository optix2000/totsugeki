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
	prediction     StatsGetPrediction
	CacheEnv       bool
	responseCache  *ResponseCache
}

type StriveAPIProxyOptions struct {
	AsyncStatsSet   bool
	PredictStatsGet bool
	CacheNews       bool
	NoNews          bool
	PredictReplay   bool
	CacheEnv        bool
	CacheFollow     bool
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

// Invalidate cache if certain requests are used
func (s *StriveAPIProxy) CacheInvalidationHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch path {
		case "/api/follow/follow_user", "/api/follow/unfollow_user":
			s.responseCache.RemoveResponse("catalog/get_follow")
			next.ServeHTTP(w, r)
		case "/api/follow/block_user", "/api/follow/unblock_user":
			s.responseCache.RemoveResponse("catalog/get_block")
			next.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// Generic handler func for cached requests
func (s *StriveAPIProxy) HandleCachedRequest(request string, w http.ResponseWriter, r *http.Request) {
	if s.responseCache.ResponseExists(request) {
		resp, body := s.responseCache.GetResponse(request)
		for name, values := range resp.Header {
			w.Header()[name] = values
		}
		w.Write(body)
	} else {
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
		buf, err := io.ReadAll(reader)
		if err != nil {
			fmt.Println(err)
		}
		s.responseCache.AddResponse(request, resp, buf)
	}
}

// GGST uses the URL from this API after initial launch so we need to intercept this.
func (s *StriveAPIProxy) HandleGetEnv(w http.ResponseWriter, r *http.Request) {
	if s.CacheEnv && s.responseCache.ResponseExists("sys/get_env") {
		resp, body := s.responseCache.GetResponse("sys/get_env")
		for name, values := range resp.Header {
			w.Header()[name] = values
		}
		w.Write(body)
	} else {
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
}

// UNSAFE: Cache news on first request. On every other request return the cached value.
func (s *StriveAPIProxy) HandleGetNews(w http.ResponseWriter, r *http.Request) {
	s.HandleCachedRequest("sys/get_news", w, r)
}

// UNSAFE: Cache get_follow on first request. On every other request return the cached value.
func (s *StriveAPIProxy) HandleGetFollow(w http.ResponseWriter, r *http.Request) {
	s.HandleCachedRequest("catalog/get_follow", w, r)
}

// UNSAFE: Cache get_block on first request. On every other request return the cached value.
func (s *StriveAPIProxy) HandleGetBlock(w http.ResponseWriter, r *http.Request) {
	s.HandleCachedRequest("catalog/get_block", w, r)
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
		CacheEnv:       false,
		responseCache: &ResponseCache{
			responses: make(map[string]*CachedResponse),
		},
	}

	statsSet := proxy.HandleCatchall
	statsGet := proxy.HandleCatchall
	getNews := proxy.HandleCatchall
	getReplay := proxy.HandleCatchall
	getFollow := proxy.HandleCatchall
	getBlock := proxy.HandleCatchall
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(proxy.CacheInvalidationHandler)

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

		proxy.prediction = CreateStatsGetPrediction(GGStriveAPIURL, &predictStatsClient, proxy.responseCache)
		r.Use(proxy.prediction.StatsGetStateHandler)
		statsGet = func(w http.ResponseWriter, r *http.Request) {
			if !proxy.prediction.HandleGetStats(w, r) {
				proxy.HandleCatchall(w, r)
			}
		}

		if options.PredictReplay {
			getReplay = statsGet
			proxy.prediction.PredictReplay = true
		}
	}
	if options.NoNews {
		getNews = func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte{})
		}
	} else if options.CacheNews {
		getNews = proxy.HandleGetNews
	}
	if options.CacheFollow {
		getFollow = proxy.HandleGetFollow
		getBlock = proxy.HandleGetBlock
	}

	if options.CacheEnv {
		proxy.CacheEnv = true
		resp, err := client.Post(GGStriveAPIURL+"sys/get_env", "application/x-www-form-urlencoded", bytes.NewBuffer([]byte("data=9295a0a002a5302e302e360391cd0100")))
		if err != nil {
			fmt.Println(err)
		}
		buf, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println(err)
		}
		buf = bytes.Replace(buf, []byte(GGStriveAPIURL), []byte(PatchedAPIURL), -1)
		proxy.responseCache.AddResponse("sys/get_env", resp, buf)
		resp.Body.Close()
	}

	r.Route("/api", func(r chi.Router) {
		r.HandleFunc("/sys/get_env", proxy.HandleGetEnv)
		r.HandleFunc("/statistics/get", statsGet)
		r.HandleFunc("/statistics/set", statsSet)
		r.HandleFunc("/tus/write", statsSet)
		r.HandleFunc("/sys/get_news", getNews)
		r.HandleFunc("/catalog/get_follow", getFollow)
		r.HandleFunc("/catalog/get_block", getBlock)
		r.HandleFunc("/catalog/get_replay", getReplay)
		r.HandleFunc("/lobby/get_vip_status", statsGet)
		r.HandleFunc("/item/get_item", statsGet)
		r.HandleFunc("/*", proxy.HandleCatchall)
	})

	proxy.Server.Handler = r
	return proxy
}
