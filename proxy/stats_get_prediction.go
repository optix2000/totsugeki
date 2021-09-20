package proxy

// Precaches statistics/get calls if the opening sequence of calls is detected

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
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
	predictionState PredictionState
	statsGetTasks   map[string]*StatsGetTask
	client          *http.Client
	PredictReplay   bool
}

type PredictionState int

// Declare typed constants each with type of status
const (
	ready PredictionState = iota
	sending_calls
)

type StatsGetType int

const (
	title_screen StatsGetType = iota
	r_code
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
		switch path {
		case "/api/statistics/get":
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewBuffer(body))
			if strings.HasSuffix(string(body), ExpectedTitleScreenCalls()[0].data+"\x00") {
				s.AsyncGetStats(body, title_screen)
				s.predictionState = sending_calls
			} else if strings.HasSuffix(string(body), ExpectedRCodeCalls()[0].data+"\x00") {
				s.AsyncGetStats(body, r_code)
				s.predictionState = sending_calls
			}
			next.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})

}

// Proxy getstats
func (s *StatsGetPrediction) HandleGetStats(w http.ResponseWriter, r *http.Request) bool {
	if s.predictionState == sending_calls {
		bodyBytes, _ := ioutil.ReadAll(r.Body)
		r.Body.Close()                                        //  must close
		r.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes)) // Reset Body as the request gets reused by catchall if this has an error.
		req := string(bodyBytes)
		if strings.HasSuffix(r.RequestURI, "catalog/get_replay") {
			regex := regexp.MustCompile(`940100059aff00.*$`)
			for _, data := range []string{"940100059aff00636390ffff000001\x00", "940100059aff00636390ffff010001\x00", "940100059aff00636390ffff020001\x00"} {
				possibleReq := regex.ReplaceAllString(req, data)
				if _, ok := s.statsGetTasks[possibleReq]; ok {
					req = possibleReq
					break
				}
			}
		}
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
			if len(s.statsGetTasks) == 0 {
				s.predictionState = ready
				fmt.Println("Done looking up stats")
			}
			return true
		}
		fmt.Println("Cache miss! " + req)
		return false
	}
	return false
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

func (s *StatsGetPrediction) AsyncGetStats(body []byte, reqType StatsGetType) {
	var reqs []StatsGetTask
	if reqType == title_screen {
		reqs = ExpectedTitleScreenCalls()
	} else {
		reqs = ExpectedRCodeCalls()
	}
	bodyConst := strings.Replace(string(body), reqs[0].data+"\x00", "", 1)

	//Clear requests from previous round
	for id := range s.statsGetTasks {
		delete(s.statsGetTasks, id)
	}

	queue := make(chan *StatsGetTask, len(reqs)+1)
	for i := range reqs {
		task := reqs[i]

		if task.path == "catalog/get_replay" && !s.PredictReplay {
			continue
		}

		id := bodyConst + task.data + "\x00"
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
		predictionState: ready,
		statsGetTasks:   make(map[string]*StatsGetTask),
		client:          client,
		PredictReplay:   false,
	}
}

func ExpectedTitleScreenCalls() []StatsGetTask {
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
		{data: "940100059aff00636390ffff000001", path: "catalog/get_replay"}, // these 3 only get used if unsafe-predict-replay is set
		{data: "940100059aff00636390ffff010001", path: "catalog/get_replay"},
		{data: "940100059aff00636390ffff020001", path: "catalog/get_replay"},
		{data: "91a0", path: "lobby/get_vip_status"},
		{data: "9105", path: "item/get_item"},
	}
}

func ExpectedRCodeCalls() []StatsGetTask {
	return []StatsGetTask{
		{data: "07ffffffff", path: "statistics/get"},
		{data: "06ff00ffff", path: "statistics/get"},
		{data: "06ff01ffff", path: "statistics/get"},
		{data: "06ff02ffff", path: "statistics/get"},
		{data: "06ff03ffff", path: "statistics/get"},
		{data: "06ff04ffff", path: "statistics/get"},
		{data: "06ff05ffff", path: "statistics/get"},
		{data: "06ff06ffff", path: "statistics/get"},
		{data: "06ff07ffff", path: "statistics/get"},
		{data: "06ff08ffff", path: "statistics/get"},
		{data: "06ff09ffff", path: "statistics/get"},
		{data: "06ff0affff", path: "statistics/get"},
		{data: "06ff0bffff", path: "statistics/get"},
		{data: "06ff0cffff", path: "statistics/get"},
		{data: "06ff0dffff", path: "statistics/get"},
		{data: "06ff0effff", path: "statistics/get"},
		{data: "06ff0fffff", path: "statistics/get"},
		{data: "06ff10ffff", path: "statistics/get"},
		{data: "06ffffffff", path: "statistics/get"},
		{data: "05ffffffff", path: "statistics/get"},
		{data: "020100ffff", path: "statistics/get"},
		{data: "020101ffff", path: "statistics/get"},
		{data: "020102ffff", path: "statistics/get"},
		{data: "020103ffff", path: "statistics/get"},
		{data: "020104ffff", path: "statistics/get"},
		{data: "020105ffff", path: "statistics/get"},
		{data: "020106ffff", path: "statistics/get"},
		{data: "020107ffff", path: "statistics/get"},
		{data: "020108ffff", path: "statistics/get"},
		{data: "020109ffff", path: "statistics/get"},
		{data: "02010affff", path: "statistics/get"},
		{data: "02010bffff", path: "statistics/get"},
		{data: "02010cffff", path: "statistics/get"},
		{data: "02010dffff", path: "statistics/get"},
		{data: "02010effff", path: "statistics/get"},
		{data: "02010fffff", path: "statistics/get"},
		{data: "020110ffff", path: "statistics/get"},
		{data: "0201ffffff", path: "statistics/get"},
		{data: "010100feff", path: "statistics/get"},
		{data: "010100ffff", path: "statistics/get"},
		{data: "010101feff", path: "statistics/get"},
		{data: "010101ffff", path: "statistics/get"},
		{data: "010102feff", path: "statistics/get"},
		{data: "010102ffff", path: "statistics/get"},
		{data: "010103feff", path: "statistics/get"},
		{data: "010103ffff", path: "statistics/get"},
		{data: "010104feff", path: "statistics/get"},
		{data: "010104ffff", path: "statistics/get"},
		{data: "010105feff", path: "statistics/get"},
		{data: "010105ffff", path: "statistics/get"},
		{data: "010106feff", path: "statistics/get"},
		{data: "010106ffff", path: "statistics/get"},
		{data: "010107feff", path: "statistics/get"},
		{data: "010107ffff", path: "statistics/get"},
		{data: "010108feff", path: "statistics/get"},
		{data: "010108ffff", path: "statistics/get"},
		{data: "010109feff", path: "statistics/get"},
		{data: "010109ffff", path: "statistics/get"},
		{data: "01010afeff", path: "statistics/get"},
		{data: "01010affff", path: "statistics/get"},
		{data: "01010bfeff", path: "statistics/get"},
		{data: "01010bffff", path: "statistics/get"},
		{data: "01010cfeff", path: "statistics/get"},
		{data: "01010cffff", path: "statistics/get"},
		{data: "01010dfeff", path: "statistics/get"},
		{data: "01010dffff", path: "statistics/get"},
		{data: "01010efeff", path: "statistics/get"},
		{data: "01010effff", path: "statistics/get"},
		{data: "01010ffeff", path: "statistics/get"},
		{data: "01010fffff", path: "statistics/get"},
		{data: "010110feff", path: "statistics/get"},
		{data: "010110ffff", path: "statistics/get"},
		{data: "0101fffeff", path: "statistics/get"},
		{data: "0101ffffff", path: "statistics/get"},
	}
}
