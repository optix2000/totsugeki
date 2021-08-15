package proxy

// Precaches statistics/get calls if the opening sequence of calls is detected

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

const StatsGetWorkers = 5

type StatsGetTask struct {
	request  string
	response chan *http.Response
}

type StatsGetPrediction struct {
	GGStriveAPIURL  string
	loginPrefix     string
	predictionState PredictionState
	statsGetTasks   map[string]*StatsGetTask
	client          *http.Client
}

type PredictionState int

// Declare typed constants each with type of status
const (
	reset PredictionState = iota
	get_env_called
	login_parsed
	sending_calls
)

type CachingResponseWriter struct {
	w    http.ResponseWriter
	buf  bytes.Buffer
	code int
}

func (rw *CachingResponseWriter) Header() http.Header {
	return rw.w.Header()
}

func (rw *CachingResponseWriter) WriteHeader(statusCode int) {
	rw.w.WriteHeader(statusCode)
	rw.code = statusCode
}

func (rw *CachingResponseWriter) Write(data []byte) (int, error) {
	rw.buf.Write(data)
	return rw.w.Write(data)
}

func (s *StatsGetPrediction) StatsGetStateHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if s.predictionState != reset &&
			path != "/api/sys/get_env" &&
			path != "/api/user/login" &&
			path != "/api/statistics/get" {
			if s.predictionState == sending_calls {
				fmt.Println("Done looking up stats")
			}
			s.predictionState = reset
		}

		if path == "/api/sys/get_env" || path == "/api/user/login" {
			wrappedWriter := CachingResponseWriter{w: w}
			next.ServeHTTP(&wrappedWriter, r)

			if path == "/api/sys/get_env" && wrappedWriter.code < 400 {
				s.predictionState = get_env_called
			} else if path == "/api/user/login" &&
				wrappedWriter.code < 400 &&
				s.predictionState == get_env_called {

				login := wrappedWriter.buf.Bytes()
				s.ParseLoginPrefix(login)
				s.predictionState = login_parsed
			}
		} else {
			next.ServeHTTP(w, r)
		}
	})

}

// Proxy getstats
func (s *StatsGetPrediction) HandleGetStats(w http.ResponseWriter, r *http.Request) bool {
	if len(s.loginPrefix) > 0 && s.predictionState == login_parsed {
		s.AsyncGetStats()
		s.predictionState = sending_calls
	}
	if len(s.loginPrefix) > 0 && s.predictionState == sending_calls {
		bodyBytes, _ := ioutil.ReadAll(r.Body)
		r.Body.Close()                                        //  must close
		r.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes)) // Reset Body as the request gets reused by catchall if this has an error.
		req := string(bodyBytes)
		if task, ok := s.statsGetTasks[req]; ok {
			resp := <-task.response
			if resp == nil {
				fmt.Println("Cache Error!")
				delete(s.statsGetTasks, req)
				return false
			}
			defer resp.Body.Close()
			// Copy headers
			for name, values := range resp.Header {
				w.Header()[name] = values
			}
			w.WriteHeader(resp.StatusCode)
			_, err := io.Copy(w, resp.Body)
			if err != nil {
				fmt.Println(err)
			}
			delete(s.statsGetTasks, req)
			return true
		}
		fmt.Println("Cache miss! " + req)
		return false
	}
	return false
}

func (s *StatsGetPrediction) ParseLoginPrefix(loginRet []byte) {
	s.loginPrefix = hex.EncodeToString(loginRet[60:79]) + hex.EncodeToString(loginRet[2:16])
}

func (s *StatsGetPrediction) BuildStatsReqBody(login string, req string) string {
	/*

		Get Stats Call Analysis
		E.g.
		data=9295xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx02a5302e302e350396a007ffffffff^@

		1.
		"data="
		length=5

		2. Header?
		9295
		length=2


		3. Login 1?
		index in login response=60
		length=19

		4. Login 2?
		index in login response=2
		length=14

		5. Divider?
		02a5
		l=2

		6. Version?
		302e302e35 (0.0.5)
		l=5

		7. Divider2?
		0396
		l=2

		8. Specific call
		e.g. a007ffffffff , Confirm that this stays between users
		l=6


		9=End
		\0
		l=1
	*/

	var sb strings.Builder
	sb.WriteString("data=")
	sb.WriteString("9295") // Header
	sb.WriteString(login)
	sb.WriteString("02a5")       // Divider
	sb.WriteString("302e302e35") // 0.0.5
	sb.WriteString("0396")       // Divider 2
	sb.WriteString(req)
	sb.WriteString("\x00") // End
	return sb.String()
}

// Process the filled queue, then exit when it's empty
func (s *StatsGetPrediction) ProcessStatsQueue(queue chan *StatsGetTask) {
	for {
		select {
		case item := <-queue:
			reqBytes := bytes.NewBuffer([]byte(item.request))
			req, err := http.NewRequest("POST", s.GGStriveAPIURL+"statistics/get", reqBytes)
			if err != nil {
				fmt.Print("Req error: ")
				fmt.Println(err)
				item.response <- nil
				continue
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Cache-Control", "no-cache")
			req.Header.Set("Cookie", "theme=theme-dark")
			req.Header.Set("User-Agent", "Steam")

			apiURL, err := url.Parse(s.GGStriveAPIURL) // TODO: Const this
			if err != nil {
				fmt.Print("Url error: ")
				fmt.Println(err)
				item.response <- nil
				continue
			}
			apiURL.Path = req.URL.Path

			req.URL = apiURL
			req.Host = ""
			req.RequestURI = ""
			res, err := s.client.Do(req)

			if err != nil {
				fmt.Print("Res error: ")
				fmt.Println(err)
				item.response <- nil
			} else {
				item.response <- res
			}
		default:
			fmt.Println("Empty queue, shutting down")
			return

		}
	}

}

func (s *StatsGetPrediction) AsyncGetStats() {
	reqs := s.ExpectedStatsGetCalls()

	queue := make(chan *StatsGetTask, len(reqs)+1)
	for _, val := range reqs {
		id := s.BuildStatsReqBody(s.loginPrefix, val)
		task := &StatsGetTask{id, make(chan *http.Response)}

		s.statsGetTasks[id] = task
		queue <- task
	}

	for i := 0; i < StatsGetWorkers; i++ {
		go s.ProcessStatsQueue(queue)
	}
}
func CreateStatsGetPrediction(GGStriveAPIURL string, client *http.Client) StatsGetPrediction {
	return StatsGetPrediction{
		GGStriveAPIURL:  GGStriveAPIURL,
		loginPrefix:     "",
		predictionState: reset,
		statsGetTasks:   make(map[string]*StatsGetTask),
		client:          client,
	}
}

func (s *StatsGetPrediction) ExpectedStatsGetCalls() []string {
	return []string{
		"a007ffffffff",
		"a009ffffffff",
		"a008ff00ffff",
		"a008ff01ffff",
		"a008ff02ffff",
		"a008ff03ffff",
		"a008ff04ffff",
		"a008ff05ffff",
		"a008ff06ffff",
		"a008ff07ffff",
		"a008ff08ffff",
		"a008ff09ffff",
		"a008ff0affff",
		"a008ff0bffff",
		"a008ff0cffff",
		"a008ff0dffff",
		"a008ff0effff",
		"a008ff0fffff",
		"a008ffffffff",
		"a006ff00ffff",
		"a006ff01ffff",
		"a006ff02ffff",
		"a006ff03ffff",
		"a006ff04ffff",
		"a006ff05ffff",
		"a006ff06ffff",
		"a006ff07ffff",
		"a006ff08ffff",
		"a006ff09ffff",
		"a006ff0affff",
		"a006ff0bffff",
		"a006ff0cffff",
		"a006ff0dffff",
		"a006ff0effff",
		"a006ff0fffff",
		"a006ffffffff",
		"a005ffffffff",
		"a0020100ffff",
		"a0020101ffff",
		"a0020102ffff",
		"a0020103ffff",
		"a0020104ffff",
		"a0020105ffff",
		"a0020106ffff",
		"a0020107ffff",
		"a0020108ffff",
		"a0020109ffff",
		"a002010affff",
		"a002010bffff",
		"a002010cffff",
		"a002010dffff",
		"a002010effff",
		"a002010fffff",
		"a00201ffffff",
		"a0010100feff",
		"a0010100ffff",
		"a0010101feff",
		"a0010101ffff",
		"a0010102feff",
		"a0010102ffff",
		"a0010103feff",
		"a0010103ffff",
		"a0010104feff",
		"a0010104ffff",
		"a0010105feff",
		"a0010105ffff",
		"a0010106feff",
		"a0010106ffff",
		"a0010107feff",
		"a0010107ffff",
		"a0010108feff",
		"a0010108ffff",
		"a0010109feff",
		"a0010109ffff",
		"a001010afeff",
		"a001010affff",
		"a001010bfeff",
		"a001010bffff",
		"a001010cfeff",
		"a001010cffff",
		"a001010dfeff",
		"a001010dffff",
		"a001010efeff",
		"a001010effff",
		"a001010ffeff",
		"a001010fffff",
		"a00101fffeff",
		"a00101ffffff",
	}
}
