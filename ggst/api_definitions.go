package ggst

type StatReqHeader struct {
	_msgpack struct{} `msgpack:",as_array"`
	UserID   string   // 18 digit User ID
	Hash     string   // 13 character response hash from /api/user/login
	Unk3     int      // Always 2
	Version  string   // Some version string. Same as ResponseHeader.Version
	Unk4     int      // Always 3
}

type StatGetReqPayload struct {
	_msgpack    struct{} `msgpack:",as_array"`
	OtherUserID string   // 18 digit User ID of other player. Empty string if self.
	Type        int      // Request type. 1: Vs stats (tension use, RC usage, perfects, etc). 2: Battle Record/Battle Chart. 6: Single player stats (eg mission, story). 7: Levels, Floor, Name, etc.  8: Unknown. 9: News.
	Unk2        int      // Set to -1 normally. Sometimes set to 1 on Type 1 requests.
	Page        int      // Seems to be page number or character ID. -1 if N/A. 0 for first page. Only used by Type 6 and 8.
	Unk3        int      // Set to -1 normally. Sometimes set to -2 on Type 1 requests.
	Unk4        int      // -1
}

type StatGetRequest struct {
	_msgpack struct{} `msgpack:",as_array"`
	Header   StatReqHeader
	Payload  StatGetReqPayload
}

type StatGetRespHeader struct {
	_msgpack  struct{} `msgpack:",as_array"`
	Hash      string   // Some sort of incrementing 13 char hash
	Unk1      int      // Unknown, always 0
	Timestamp string   // Current time in "YYYY/MM/DD HH:MM:SS" in UTC
	Version1  string   // Some version string. "0.1.1" in v1.16. "0.0.7" in v1.10. "0.0.6" in v1.07. "0.0.5" in v1.06, was "0.0.4" in v1.05
	Version2  string   // Another version string. Always 0.0.2
	Version3  string   // Another version string. Always 0.0.2
	Unk2      string   // Unknown, empty string
	Unk3      string   // Unknown, empty string
}

type Payload struct {
	_msgpack struct{} `msgpack:",as_array"`
	Unk1     int      // Unknown, always 0.
	Payload  map[string]interface{}
}

type StatGetRespPayload struct {
	_msgpack struct{} `msgpack:",as_array"`
	Unk1     int      // Unknown, always 0.
	JSON     RawJSON
}

type RawJSON map[string]interface{}

type StatGetResponse struct {
	_msgpack struct{} `msgpack:",as_array"`
	Header   StatGetRespHeader
	Payload  StatGetRespPayload
}
