package dto

import "strconv"

type Mapped map[string]interface{}

func MustMap(p interface{}) Mapped {
	switch v := p.(type) {
	case map[string]interface{}:
		return v
	default:
		return Mapped{}
	}
}

func (r Mapped) MustStringMap(key string) map[string]string {
	res := make(map[string]string)
	switch v := r[key].(type) {
	case map[string]interface{}:
		for k, item := range v {
			if val, ok := item.(string); ok {
				res[k] = val
			} else {
				res[k] = ""
			}
		}
		return res
	default:
		return res
	}
}

func (r Mapped) MustInt(key string) uint64 {
	switch v := r[key].(type) {
	case string:
		if len(v) >= 2 && (v[:2] == "0x" || v[:2] == "0X") {
			v, _ := strconv.ParseUint(v[2:], 16, 64)
			return v
		}
		return 0
	case uint64:
		return v
	case float64:
		return uint64(v)
	default:
		return 0
	}
}

func (r Mapped) Get(key string) Mapped {
	switch v := r[key].(type) {
	case map[string]interface{}:
		return v
	default:
		return Mapped{}
	}
}

func (r Mapped) GetInterface(key string) interface{} {
	if v, ok := r[key]; ok {
		return v
	} else {
		return nil
	}
}

func (r Mapped) GetSlice(key string) []Mapped {
	switch v := r[key].(type) {
	case []interface{}:
		var res []Mapped
		for i := 0; i < len(v); i++ {
			rm, ok := v[i].(map[string]interface{})
			if ok {
				res = append(res, rm)
			} else {
				res = append(res, Mapped{})
			}
		}
		return res
	default:
		return []Mapped{}
	}
}

func (r Mapped) MustStrings(key string) []string {
	switch v := r[key].(type) {
	case []interface{}:
		var res []string
		for _, item := range v {
			_, ok := item.(string)
			if ok {
				res = append(res, item.(string))
			}
		}
		return res
	default:
		return []string{}
	}
}

func (r Mapped) MustString(key string) string {
	n, ok := r[key].(string)
	if ok {
		return n
	} else {
		return ""
	}
}
