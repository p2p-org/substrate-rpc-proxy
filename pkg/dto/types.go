package dto

import (
	"net/http"

	jsoniter "github.com/json-iterator/go"
)

type Params interface{}

type RPCBatch []RPCFrame

func (b RPCBatch) Render(w http.ResponseWriter, r *http.Request) error {
	return nil
}

type RPCFrame struct {
	Id     int         `json:"id"`
	RPC    string      `json:"jsonrpc"`
	Method string      `json:"method,omitempty"`
	Raw    string      `json:"raw,omitempty"`
	Params Params      `json:"params,omitempty"`
	Result interface{} `json:"result,omitempty"`
	Error  *RPCError   `json:"error,omitempty"`
}

func NewFrameFrom(payload []byte) (*RPCFrame, error) {
	var f RPCFrame
	if err := jsoniter.Unmarshal(payload, &f); err != nil {
		return nil, err
	}
	f.Raw = string(payload)
	return &f, nil
}

func (r *RPCFrame) MappedResult() Mapped {
	switch v := r.Result.(type) {
	case map[string]interface{}:
		return v
	default:
		return Mapped{}
	}
}

func (r *RPCFrame) StringResult() string {
	switch v := r.Result.(type) {
	case string:
		return v
	default:
		return ""
	}
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type Block struct {
	Raw        string
	Number     uint64
	ParentHash string
	Hash       string
	Extrinsics []string
}

type ExtrinsicData struct {
	Payload          string `json:"raw"`
	Hash             string `json:"hash"`
	Era              string `json:"era"`
	LifetimeInBlocks uint64 `json:"lifetime"`
	FirstSeenBlockNo uint64 `json:"firstSeenBlockNo"`
}

type Event struct {
	Type         EventType   `json:"type"`
	ExtrinsicIdx int         `json:"extrinsic_idx"`
	Params       interface{} `json:"params"`
}

type EventType struct {
	ModuleId string `json:"module_id"`
	EventId  string `json:"event_id"`
}

type StoredMessage struct {
	RPCFrame          *RPCFrame      `json:"rpcFrame"`
	Extrinsic         *ExtrinsicData `json:"extrinsic"`
	ContainsExtrinsic bool           `json:",omitempty"`
}
