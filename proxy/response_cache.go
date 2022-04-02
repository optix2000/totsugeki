package proxy

import "net/http"

type CachedResponse struct {
	response *http.Response
	body     []byte
}

type ResponseCache struct {
	responses map[string]*CachedResponse
}

func (c *ResponseCache) ResponseExists(request string) bool {
	_, exists := c.responses[request]
	return exists
}

func (c *ResponseCache) GetResponse(request string) (http.Response, []byte) {
	response := c.responses[request]
	return *response.response, response.body
}

func (c *ResponseCache) AddResponse(request string, response *http.Response, body []byte) {
	c.responses[request] = &CachedResponse{
		response: response,
		body:     body,
	}
}

func (c *ResponseCache) RemoveResponse(request string) {
	delete(c.responses, request)
}
