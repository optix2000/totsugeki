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
	data     string
	path     string
	request  string
	response chan *http.Response
}

type StatsGetPrediction struct {
	GGStriveAPIURL  string
	loginPrefix     string
	apiVersion      string
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
	finished
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
		if s.predictionState != finished && path == "/api/sys/get_news" {
			if s.predictionState == sending_calls {
				fmt.Println("Done looking up stats")
			}
			s.predictionState = finished
		}

		if (path == "/api/sys/get_env" || path == "/api/user/login") && s.predictionState != finished {
			wrappedWriter := CachingResponseWriter{w: w}
			next.ServeHTTP(&wrappedWriter, r)

			if path == "/api/sys/get_env" && wrappedWriter.code < 400 {
				body := wrappedWriter.buf.Bytes()
				s.ParseApiVersion(body)
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

func (s *StatsGetPrediction) ParseApiVersion(getEnvBody []byte) {
	s.apiVersion = hex.EncodeToString([]byte(strings.Split(string(getEnvBody), "\xa5")[1]))
}

func (s *StatsGetPrediction) BuildStatsReqBody(login string, req string, apiVersion string) string {
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
		03
		l=1

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
	sb.WriteString("02a5")     // Divider
	sb.WriteString(apiVersion) // 0.0.5
	sb.WriteString("03")       // Divider 2
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
			req, err := http.NewRequest("POST", s.GGStriveAPIURL+item.path, reqBytes)
			if err != nil {
				fmt.Print("Req error: ")
				fmt.Println(err)
				item.response <- nil
				continue
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Cache-Control", "no-cache")
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
	for i := range reqs {
		task := reqs[i]
		id := s.BuildStatsReqBody(s.loginPrefix, task.data, s.apiVersion)
		task.request = id
		task.response = make(chan *http.Response)

		s.statsGetTasks[id] = &task
		queue <- &task
	}

	for i := 0; i < StatsGetWorkers; i++ {
		go s.ProcessStatsQueue(queue)
	}
}
func CreateStatsGetPrediction(GGStriveAPIURL string, client *http.Client) StatsGetPrediction {
	return StatsGetPrediction{
		GGStriveAPIURL:  GGStriveAPIURL,
		loginPrefix:     "",
		apiVersion:      hex.EncodeToString([]byte("0.0.6")),
		predictionState: reset,
		statsGetTasks:   make(map[string]*StatsGetTask),
		client:          client,
	}
}

func (s *StatsGetPrediction) ExpectedStatsGetCalls() []StatsGetTask {
	return []StatsGetTask{
		{data: "96a007ffffffff", path: "statistics/get"},
		{data: "96a009ffffffff", path: "statistics/get"},
		{data: "96a008ff00ffff", path: "statistics/get"},
		{data: "96a008ff01ffff", path: "statistics/get"},
		{data: "96a008ff02ffff", path: "statistics/get"},
		{data: "96a008ff03ffff", path: "statistics/get"},
		{data: "96a008ff04ffff", path: "statistics/get"},
		{data: "96a008ff05ffff", path: "statistics/get"},
		{data: "96a008ff06ffff", path: "statistics/get"},
		{data: "96a008ff07ffff", path: "statistics/get"},
		{data: "96a008ff08ffff", path: "statistics/get"},
		{data: "96a008ff09ffff", path: "statistics/get"},
		{data: "96a008ff0affff", path: "statistics/get"},
		{data: "96a008ff0bffff", path: "statistics/get"},
		{data: "96a008ff0cffff", path: "statistics/get"},
		{data: "96a008ff0dffff", path: "statistics/get"},
		{data: "96a008ff0effff", path: "statistics/get"},
		{data: "96a008ff0fffff", path: "statistics/get"},
		{data: "96a008ff10ffff", path: "statistics/get"},
		{data: "96a008ffffffff", path: "statistics/get"},
		{data: "96a006ff00ffff", path: "statistics/get"},
		{data: "96a006ff01ffff", path: "statistics/get"},
		{data: "96a006ff02ffff", path: "statistics/get"},
		{data: "96a006ff03ffff", path: "statistics/get"},
		{data: "96a006ff04ffff", path: "statistics/get"},
		{data: "96a006ff05ffff", path: "statistics/get"},
		{data: "96a006ff06ffff", path: "statistics/get"},
		{data: "96a006ff07ffff", path: "statistics/get"},
		{data: "96a006ff08ffff", path: "statistics/get"},
		{data: "96a006ff09ffff", path: "statistics/get"},
		{data: "96a006ff0affff", path: "statistics/get"},
		{data: "96a006ff0bffff", path: "statistics/get"},
		{data: "96a006ff0cffff", path: "statistics/get"},
		{data: "96a006ff0dffff", path: "statistics/get"},
		{data: "96a006ff0effff", path: "statistics/get"},
		{data: "96a006ff0fffff", path: "statistics/get"},
		{data: "96a006ff10ffff", path: "statistics/get"},
		{data: "96a006ffffffff", path: "statistics/get"},
		{data: "96a005ffffffff", path: "statistics/get"},
		{data: "96a0020100ffff", path: "statistics/get"},
		{data: "96a0020101ffff", path: "statistics/get"},
		{data: "96a0020102ffff", path: "statistics/get"},
		{data: "96a0020103ffff", path: "statistics/get"},
		{data: "96a0020104ffff", path: "statistics/get"},
		{data: "96a0020105ffff", path: "statistics/get"},
		{data: "96a0020106ffff", path: "statistics/get"},
		{data: "96a0020107ffff", path: "statistics/get"},
		{data: "96a0020108ffff", path: "statistics/get"},
		{data: "96a0020109ffff", path: "statistics/get"},
		{data: "96a002010affff", path: "statistics/get"},
		{data: "96a002010bffff", path: "statistics/get"},
		{data: "96a002010cffff", path: "statistics/get"},
		{data: "96a002010dffff", path: "statistics/get"},
		{data: "96a002010effff", path: "statistics/get"},
		{data: "96a002010fffff", path: "statistics/get"},
		{data: "96a0020110ffff", path: "statistics/get"},
		{data: "96a00201ffffff", path: "statistics/get"},
		{data: "96a0010100feff", path: "statistics/get"},
		{data: "96a0010100ffff", path: "statistics/get"},
		{data: "96a0010101feff", path: "statistics/get"},
		{data: "96a0010101ffff", path: "statistics/get"},
		{data: "96a0010102feff", path: "statistics/get"},
		{data: "96a0010102ffff", path: "statistics/get"},
		{data: "96a0010103feff", path: "statistics/get"},
		{data: "96a0010103ffff", path: "statistics/get"},
		{data: "96a0010104feff", path: "statistics/get"},
		{data: "96a0010104ffff", path: "statistics/get"},
		{data: "96a0010105feff", path: "statistics/get"},
		{data: "96a0010105ffff", path: "statistics/get"},
		{data: "96a0010106feff", path: "statistics/get"},
		{data: "96a0010106ffff", path: "statistics/get"},
		{data: "96a0010107feff", path: "statistics/get"},
		{data: "96a0010107ffff", path: "statistics/get"},
		{data: "96a0010108feff", path: "statistics/get"},
		{data: "96a0010108ffff", path: "statistics/get"},
		{data: "96a0010109feff", path: "statistics/get"},
		{data: "96a0010109ffff", path: "statistics/get"},
		{data: "96a001010afeff", path: "statistics/get"},
		{data: "96a001010affff", path: "statistics/get"},
		{data: "96a001010bfeff", path: "statistics/get"},
		{data: "96a001010bffff", path: "statistics/get"},
		{data: "96a001010cfeff", path: "statistics/get"},
		{data: "96a001010cffff", path: "statistics/get"},
		{data: "96a001010dfeff", path: "statistics/get"},
		{data: "96a001010dffff", path: "statistics/get"},
		{data: "96a001010efeff", path: "statistics/get"},
		{data: "96a001010effff", path: "statistics/get"},
		{data: "96a001010ffeff", path: "statistics/get"},
		{data: "96a001010fffff", path: "statistics/get"},
		{data: "96a0010110feff", path: "statistics/get"},
		{data: "96a0010110ffff", path: "statistics/get"},
		{data: "96a00101fffeff", path: "statistics/get"},
		{data: "96a00101ffffff", path: "statistics/get"},
		{data: "93000101", path: "catalog/get_follow"},
		{data: "920101", path: "catalog/get_block"},
		{data: "91a0", path: "lobby/get_vip_status"},
		{data: "9105", path: "item/get_item"},
	}
}
