package worker

import "encoding/json"

func jsonNew(b []byte, v any) error { return json.Unmarshal(b, v) }
