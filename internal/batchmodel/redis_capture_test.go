package batchmodel

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeOperationHashFieldBindsStoredRequestAndIdentity(t *testing.T) {
	t.Parallel()
	request := `{"schema":1,"table":"t1","epoch":"1","expected_table_revision":"12","operation_id":"op:1","actor":"w1","members":[{"id":"m1","expect":{"revision":"7"}}]}`
	h := sha1.Sum([]byte(request))
	fields := map[string]string{"operation_id": "op:1", "digest": hex.EncodeToString(h[:]), "request": request,
		"stream_id": "10-0", "epoch": "1", "rev_before": "12", "rev_after": "13", "outcome": "changed", "result": "[]"}
	encode := func(v map[string]string) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	got, err := decodeOperationRecord("t1", "1:op:1", encode(fields))
	if err != nil || got.OperationID != "op:1" || string(got.Canonical) != request || string(got.ResultJSON) != "[]" {
		t.Fatalf("valid hash field: %+v, %v", got, err)
	}
	for name, change := range map[string]func(map[string]string){
		"extra field":  func(v map[string]string) { v["hidden"] = "x" },
		"wrong digest": func(v map[string]string) { v["digest"] = strings.Repeat("0", 40) },
		"wrong request epoch": func(v map[string]string) {
			v["request"] = strings.Replace(v["request"], `"epoch":"1"`, `"epoch":"2"`, 1)
		},
		"wrong record id": func(v map[string]string) { v["operation_id"] = "other" },
		"invalid result":  func(v map[string]string) { v["result"] = "{" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := make(map[string]string, len(fields))
			for k, v := range fields {
				copy[k] = v
			}
			change(copy)
			if _, err := decodeOperationRecord("t1", "1:op:1", encode(copy)); err == nil {
				t.Fatal("malformed durable record accepted")
			}
		})
	}
	if _, err := decodeOperationRecord("t1", "hidden", encode(fields)); err == nil {
		t.Fatal("malformed hash field accepted")
	}
	if _, err := decodeOperationRecord("t1", "1:op:1", `{"operation_id":"op:1","operation_id":"op:1"}`); err == nil {
		t.Fatal("duplicate JSON field accepted")
	}
}
