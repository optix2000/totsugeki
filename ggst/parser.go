package ggst

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/vmihailenco/msgpack/v5"
)

type Decoder struct {
	d *msgpack.Decoder
}

func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{d: msgpack.NewDecoder(r)}
}

func (dec *Decoder) Decode(v interface{}) error {
	return dec.d.Decode(v)
}

type Encoder struct {
	e *msgpack.Encoder
}

func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{e: msgpack.NewEncoder(w)}
}

func (dec *Encoder) Encode(v interface{}) error {
	return dec.e.Encode(v)
}

func Unmarshal(data []byte, v interface{}) error {
	return msgpack.Unmarshal(data, v)
}

func Marshal(v interface{}) ([]byte, error) {
	return msgpack.Marshal(v)
}

func UnmarshalStatResp(data []byte) (*StatGetResponse, error) {
	parsedResp := &StatGetResponse{}
	err := Unmarshal(data, parsedResp)
	if err != nil {
		return nil, err
	}
	return parsedResp, err
}

func (J *RawJSON) DecodeMsgpack(dec *msgpack.Decoder) error {
	s, err := dec.DecodeString()
	if err != nil {
		return err
	}
	jDecoder := json.NewDecoder(bytes.NewBufferString(s))
	jDecoder.UseNumber() // Things like AccountID are very long numbers
	err = jDecoder.Decode(J)
	if err != nil {
		return err
	}
	return nil
}

func (J *RawJSON) EncodeMsgpack(enc *msgpack.Encoder) error {
	b, err := json.Marshal(J)
	if err != nil {
		return err
	}
	return enc.EncodeString(string(b))
}
