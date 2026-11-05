package catalog

import (
	"encoding/json"
	"testing"
)

// Searches a person starts in Box Office say so, so their upgrades don't
// follow the automatic upgrade rules; others don't.
func TestSearchPayload(t *testing.T) {
	var v struct {
		ID     int64 `json:"id"`
		ByHand bool  `json:"byHand"`
	}
	if err := json.Unmarshal([]byte(searchPayload(5, true)), &v); err != nil || v.ID != 5 || !v.ByHand {
		t.Fatalf("by hand %+v %v", v, err)
	}
	v.ByHand = false
	if err := json.Unmarshal([]byte(searchPayload(6, false)), &v); err != nil || v.ID != 6 || v.ByHand {
		t.Fatalf("not by hand %+v %v", v, err)
	}
}
