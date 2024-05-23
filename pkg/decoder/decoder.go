package decoder

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"

	scale "github.com/itering/scale.go"
	"github.com/itering/scale.go/types"
	"github.com/itering/scale.go/types/scaleBytes"
	"github.com/itering/scale.go/utiles"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
)

type Decoder struct {
	log           *logrus.Logger
	metadata      *types.MetadataStruct
	ss58Prefix    uint16
	ChainSpecName string
}

func NewDecoder(l *logrus.Logger, metadata string) (*Decoder, error) {
	d := Decoder{
		log: l,
	}
	var err error

	meta := &scale.MetadataDecoder{}
	meta.Init(utiles.HexToBytes(metadata))
	err = meta.Process()
	if err != nil {
		return nil, err
	}
	d.metadata = &meta.Metadata
	for _, c := range d.metadata.Metadata.Modules[0].Constants {
		if c.Name == "SS58Prefix" {
			scale := types.ScaleDecoder{}
			scale.Init(scaleBytes.ScaleBytes{Data: utiles.HexToBytes(c.ConstantsValue)}, nil)
			d.ss58Prefix = scale.ProcessAndUpdateData(c.Type).(uint16)
		}
		if c.Name == "Version" {
			scale := types.ScaleDecoder{}
			scale.Init(scaleBytes.ScaleBytes{Data: utiles.HexToBytes(c.ConstantsValue)}, nil)
			d.ChainSpecName = dto.MustMap(scale.ProcessAndUpdateData(c.Type)).MustString("spec_name")
		}
	}
	//d.scaleOpts = &types.ScaleDecoderOption{Metadata: &meta.Metadata}
	return &d, nil
}

func (d *Decoder) GetExtrinsicFromParams(ctx context.Context, params interface{}) (*dto.Extrinsic, error) {
	var hexparams []string
	if params != nil {
		hexparams = d.readAllHexParams(ctx, params)
	} else {
		// no params to decode
		return nil, nil
	}
	for _, p := range hexparams {
		// transaction min len can not be less than 80
		// https://wiki.polkadot.network/docs/build-transaction-construction#transaction-format
		if len(p) >= 160 {
			defer func() {
				if r := recover(); r != nil {
					if s, ok := r.(string); !ok || !strings.Contains(s, "Extrinsics version") {
						d.log.Warn(r)
					}
				}
			}()
			scale := scale.ExtrinsicDecoder{}
			scale.Init(scaleBytes.ScaleBytes{Data: utiles.HexToBytes(p)}, &types.ScaleDecoderOption{Metadata: d.metadata})
			scale.Process()
			period, _, _ := DecodeExtrinsicEra(scale.Era, 0)
			return &dto.Extrinsic{
				Payload:          p,
				Hash:             scale.ExtrinsicHash,
				Era:              scale.Era,
				LifetimeInBlocks: uint64(period),
			}, nil
		}
	}
	return nil, nil
}

func (d *Decoder) DecodeEvents(ctx context.Context, payload string) []dto.Event {
	decoder := scale.EventsDecoder{}
	defer func() {
		if r := recover(); r != nil {
			d.log.Warn(r)
		}
	}()
	decoder.Init(scaleBytes.ScaleBytes{Data: utiles.HexToBytes(payload)}, &types.ScaleDecoderOption{Metadata: d.metadata})
	decoder.Process()

	switch v := decoder.Value.(type) {
	case []interface{}:
		var events []dto.Event
		for _, eventRecord := range v {

			e := eventRecord.(map[string]interface{})
			b, _ := jsoniter.Marshal(e["params"])
			var p interface{}
			jsoniter.Unmarshal(b, &p)
			events = append(events, dto.Event{
				Type: dto.EventType{
					ModuleId: e["module_id"].(string),
					EventId:  e["event_id"].(string),
				},
				ExtrinsicIdx: e["extrinsic_idx"].(int),
				Params:       p,
			})
			//d.log.Debugf("decoded new event %v", events[len(events)-1].Type)
		}
		return events
	default:
		return nil
	}
}

type StorageRequest struct {
	Module     string
	Method     string
	StorageKey string
	StoredType string
}

// Works only with Metadata V14
func (d *Decoder) NewStorageRequest(module, method string, args ...interface{}) (*StorageRequest, error) {
	var sc StorageRequest
	for _, runtimeModule := range d.metadata.Metadata.Modules {
		if strings.EqualFold(runtimeModule.Name, module) {
			for _, runtimeMethod := range runtimeModule.Storage {
				if strings.EqualFold(runtimeMethod.Name, method) {
					sc.Module = runtimeModule.Name
					sc.Method = runtimeMethod.Name
					storageKey := append(TwoxHash([]byte(sc.Module), 128), TwoxHash([]byte(sc.Method), 128)...)
					switch t := runtimeMethod.Type; t.Origin {
					// args must be same as keyVec
					case "Map":
						sc.StoredType = t.NMapType.Value
						argCount := 0
						if len(args) <= len(t.NMapType.KeyVec) {
							argCount = len(args)
						} else {
							return nil, fmt.Errorf("too many arguments for %s.%s", sc.Module, sc.Method)
						}
						for i := 0; i < argCount; i++ {
							arg := types.Encode(t.NMapType.KeyVec[i], args[i])
							storageKey = append(storageKey, DoHash(utiles.HexToBytes(arg), t.NMapType.Hashers[i])...)
						}
					// no args, only response
					case "PlainType":
						sc.StoredType = *t.PlainType
					}
					sc.StorageKey = fmt.Sprintf("0x%s", utiles.BytesToHex(storageKey))
					return &sc, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("storage request %s.%s not found in runtime", module, method)
}

func (r *StorageRequest) DecodeResponse(payload string) dto.Params {
	scale := types.ScaleDecoder{}
	defer func() {
		if rec := recover(); rec != nil {
			return
		}
	}()
	scale.Init(scaleBytes.ScaleBytes{Data: utiles.HexToBytes(payload)}, nil)
	return scale.ProcessAndUpdateData(r.StoredType)
}

type Params interface{}

func (d *Decoder) DecodeExtrinsic(ctx context.Context, payload string) dto.Mapped {
	decoder := scale.ExtrinsicDecoder{}
	defer func() {
		if r := recover(); r != nil {
			d.log.Warn(r)
		}
	}()

	decoder.Init(scaleBytes.ScaleBytes{Data: utiles.HexToBytes(payload)}, &types.ScaleDecoderOption{Metadata: d.metadata})
	decoder.Process()
	if b, err := jsoniter.Marshal(decoder.Value); err == nil {
		var result dto.Mapped
		jsoniter.Unmarshal(b, &result)
		return result
	}
	return nil
}

func (d *Decoder) readAllHexParams(ctx context.Context, params dto.Params) []string {
	if params == nil {
		return []string{}
	}
	current := []string{}
	_, ok := params.([]interface{})
	if ok {
		for _, p := range params.([]interface{}) {
			// we dont care about numbers
			_, isNumber := p.(float64)
			if isNumber {
				continue
			}
			strVal, isStr := p.(string)
			if isStr {
				if strings.HasPrefix(strVal, "0x") {
					current = append(current, strVal)
				} else {
					// we dont care about non-hex values
					continue
				}
			} else {
				nested := d.readAllHexParams(ctx, p)
				current = append(current, nested...)
			}
		}
	} else {
		d.log.Debugf("unsupported params type %v", params)
		// TODO add some logging when params not a list of strings
	}
	return current
}

func DecodeExtrinsicEra(era string, current uint64) (uint64, uint64, uint64) {
	ebytes := utiles.HexToBytes(era)
	first := uint64(ebytes[0])
	second := uint64(ebytes[1])
	encoded := first + (second << 8)
	var period uint64 = 2 << (encoded % (1 << 4))
	quantizeFactor := math.Max(float64(period>>12), 1)
	phase := (encoded >> 4) * uint64(quantizeFactor)
	var birth uint64
	if current != 0 && period != 0 {
		var t uint64
		if current-phase > 0 {
			t = current
		}
		birth = (t - phase) / period
	}
	return period, phase, birth
}
