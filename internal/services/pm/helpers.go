package pm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func newPMRequestID(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "pm"
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
