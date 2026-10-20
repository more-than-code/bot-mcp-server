package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHealthcheckShape(t *testing.T) {
	const sentinel = "argv-sentinel-do-not-echo"
	many := `["` + sentinel + `"`
	for i := 0; i < 32; i++ {
		many += `,"ok"`
	}
	many += `]`
	long := strings.Repeat("b", 1025)
	edge := strings.Repeat("c", 1024)
	cases := []struct {
		spec string
		want string
	}{
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["redis-cli","ping"]}}`, ""},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["` + edge + `"],"interval_sec":5,"timeout_sec":1,"retries":1,"start_period_sec":0}}`, ""},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"interval_sec":300,"timeout_sec":60,"retries":10,"start_period_sec":600}}`, ""},
		{`{"kind":"service","image":"nginx","healthcheck":null}`, ""},
		{`{"kind":"service","image":"nginx"}`, ""},
		{`{"kind":"job","image":"nginx","healthcheck":{"test":["` + sentinel + `"]}}`, ""},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"interval_sec":5,"timeout_sec":60}}`, ""},
		{`{"kind":"service","image":"nginx","healthcheck":{}}`, "healthcheck.test is required"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":null}}`, "healthcheck.test is required"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":[]}}`, "healthcheck.test must be 1 to 32 strings"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":` + many + `}}`, "healthcheck.test must be 1 to 32 strings"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["` + sentinel + `",""]}}`, "healthcheck.test items must be 1 to 1024 bytes"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["` + long + `"]}}`, "healthcheck.test items must be 1 to 1024 bytes"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":"` + sentinel + `"}}`, "healthcheck.test must be 1 to 32 strings"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":[1]}}`, "healthcheck.test must be 1 to 32 strings"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"interval_sec":4}}`, "healthcheck.interval_sec must be an integer from 5 to 300"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"interval_sec":301}}`, "healthcheck.interval_sec must be an integer from 5 to 300"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"interval_sec":30.0}}`, "healthcheck.interval_sec must be an integer from 5 to 300"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"interval_sec":null}}`, "healthcheck.interval_sec must be an integer from 5 to 300"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"timeout_sec":0}}`, "healthcheck.timeout_sec must be an integer from 1 to 60"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"timeout_sec":61}}`, "healthcheck.timeout_sec must be an integer from 1 to 60"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"retries":0}}`, "healthcheck.retries must be an integer from 1 to 10"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"retries":11}}`, "healthcheck.retries must be an integer from 1 to 10"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"retries":true}}`, "healthcheck.retries must be an integer from 1 to 10"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"start_period_sec":-1}}`, "healthcheck.start_period_sec must be an integer from 0 to 600"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"start_period_sec":601}}`, "healthcheck.start_period_sec must be an integer from 0 to 600"},
		{`{"kind":"service","image":"nginx","healthcheck":{"test":["ok"],"shell":"` + sentinel + `"}}`, "unknown healthcheck field shell"},
		{`{"kind":"service","image":"nginx","healthcheck":"` + sentinel + `"}`, "healthcheck must be an object"},
		{`{"kind":"service","image":"nginx","healthcheck":["` + sentinel + `"]}`, "healthcheck must be an object"},
	}
	for _, tc := range cases {
		err := validateImageSpec(json.RawMessage(tc.spec))
		if tc.want == "" {
			if err != nil {
				t.Fatalf("%s: %v", tc.spec, err)
			}
			continue
		}
		if err == nil || err.Error() != tc.want || strings.Contains(err.Error(), sentinel) {
			t.Fatalf("spec %s err %v want %s", tc.spec, err, tc.want)
		}
	}
}

func TestProjectServiceHealthSource(t *testing.T) {
	decode := func(raw string) any {
		t.Helper()
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		var row map[string]any
		if err := dec.Decode(&row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	docker, ok := projectServiceHealth(decode(`{"status":"down","last_checked_at":"2026-10-20T00:00:00Z","source":"docker","latency_ms":null,"note":"secret"}`))
	if !ok || docker["status"] != "down" || docker["source"] != "docker" {
		t.Fatalf("docker %#v", docker)
	}
	if v, present := docker["latency_ms"]; !present || v != nil {
		t.Fatalf("docker latency %#v", docker)
	}
	if _, present := docker["note"]; present {
		t.Fatal("kept note")
	}
	check, _ := projectServiceHealth(decode(`{"status":"up","source":"check","latency_ms":12}`))
	if check["source"] != "check" || check["latency_ms"] != json.Number("12") {
		t.Fatalf("check %#v", check)
	}
	none, _ := projectServiceHealth(decode(`{"status":"unknown","source":null,"latency_ms":null}`))
	if v, present := none["source"]; !present || v != nil {
		t.Fatalf("null source %#v", none)
	}
	if v, present := none["latency_ms"]; !present || v != nil {
		t.Fatalf("null latency %#v", none)
	}
	bare, _ := projectServiceHealth(decode(`{"status":"up"}`))
	if _, present := bare["source"]; present {
		t.Fatal("invented source")
	}
	if _, present := bare["latency_ms"]; present {
		t.Fatal("invented latency")
	}
	other, _ := projectServiceHealth(decode(`{"status":"up","source":"nope","latency_ms":"4"}`))
	if _, present := other["source"]; present {
		t.Fatalf("kept source %#v", other)
	}
	if _, present := other["latency_ms"]; present {
		t.Fatalf("kept latency %#v", other)
	}

	out, err := projectServiceHealthChecks([]byte(`{"name":"redis","ports":[6379],"health":{"status":"up","source":null,"latency_ms":null,"note":"secret"},"health_checks":[],"token":"caller-token"}`))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, `"source":null`) || !strings.Contains(text, `"latency_ms":null`) || strings.Contains(text, "secret") || strings.Contains(text, "caller-token") {
		t.Fatalf("projected %s", text)
	}
}
