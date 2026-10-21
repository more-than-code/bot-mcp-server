package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoggingSchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(catalogWriteSchema), &schema); err != nil {
		t.Fatal(err)
	}
	props, _ := schema["properties"].(map[string]any)
	spec, _ := props["spec"].(map[string]any)
	if spec["additionalProperties"] != false {
		t.Fatalf("spec additionalProperties %#v", spec["additionalProperties"])
	}
	specProps, _ := spec["properties"].(map[string]any)
	logging, ok := specProps["logging"].(map[string]any)
	if !ok {
		t.Fatal("logging missing from spec schema")
	}
	types, _ := logging["type"].([]any)
	if len(types) != 2 || types[0] != "object" || types[1] != "null" {
		t.Fatalf("type %#v", logging["type"])
	}
	if logging["additionalProperties"] != false {
		t.Fatalf("additionalProperties %#v", logging["additionalProperties"])
	}
	required, _ := logging["required"].([]any)
	if len(required) != 2 || required[0] != "max_size_mb" || required[1] != "max_files" {
		t.Fatalf("required %#v", required)
	}
	fields, _ := logging["properties"].(map[string]any)
	if len(fields) != 2 {
		t.Fatalf("properties %#v", fields)
	}
	size, _ := fields["max_size_mb"].(map[string]any)
	files, _ := fields["max_files"].(map[string]any)
	if size["type"] != "integer" || size["minimum"] != float64(1) || size["maximum"] != float64(100) {
		t.Fatalf("max_size_mb %#v", size)
	}
	if files["type"] != "integer" || files["minimum"] != float64(1) || files["maximum"] != float64(10) {
		t.Fatalf("max_files %#v", files)
	}
}

func TestLoggingShape(t *testing.T) {
	const sentinel = "log-sentinel-do-not-echo"
	cases := []struct {
		spec string
		want string
	}{
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":1,"max_files":1}}`, ""},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":100,"max_files":10}}`, ""},
		{`{"kind":"job","image":"nginx","logging":{"max_size_mb":10,"max_files":3}}`, ""},
		{`{"kind":"service","image":"nginx","logging":null}`, ""},
		{`{"kind":"service","image":"nginx"}`, ""},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":5,"max_files":2},"healthcheck":{"test":["ok"]}}`, ""},
		{`{"kind":"service","image":"nginx","logging":{}}`, "logging.max_size_mb is required"},
		{`{"kind":"service","image":"nginx","logging":{"max_files":3}}`, "logging.max_size_mb is required"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":10}}`, "logging.max_files is required"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":null,"max_files":3}}`, "logging.max_size_mb must be an integer from 1 to 100"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":0,"max_files":3}}`, "logging.max_size_mb must be an integer from 1 to 100"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":101,"max_files":3}}`, "logging.max_size_mb must be an integer from 1 to 100"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":10.0,"max_files":3}}`, "logging.max_size_mb must be an integer from 1 to 100"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":"` + sentinel + `","max_files":3}}`, "logging.max_size_mb must be an integer from 1 to 100"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":true,"max_files":3}}`, "logging.max_size_mb must be an integer from 1 to 100"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":10,"max_files":0}}`, "logging.max_files must be an integer from 1 to 10"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":10,"max_files":11}}`, "logging.max_files must be an integer from 1 to 10"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":10,"max_files":1.0}}`, "logging.max_files must be an integer from 1 to 10"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":10,"max_files":null}}`, "logging.max_files must be an integer from 1 to 10"},
		{`{"kind":"service","image":"nginx","logging":{"max_size_mb":10,"max_files":3,"driver":"` + sentinel + `"}}`, "unknown logging field driver"},
		{`{"kind":"service","image":"nginx","logging":"` + sentinel + `"}`, "logging must be an object"},
		{`{"kind":"service","image":"nginx","logging":["` + sentinel + `"]}`, "logging must be an object"},
		{`{"kind":"service","image":"nginx","logging":5}`, "logging must be an object"},
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
		if strings.Contains(tc.spec, "101") && strings.Contains(err.Error(), "101") {
			t.Fatalf("error echoed the value: %v", err)
		}
	}
}

func TestProjectCatalogLogging(t *testing.T) {
	const (
		progLeak  = "program-log-do-not-keep"
		extraLeak = "image-log-extra-do-not-keep"
		strLeak   = "image-log-string-do-not-keep"
	)
	raw := []byte(`{"items":[
		{"name":"tra-backend","kind":"program","command_display":"pnpm start","my_actions":["run"],
		 "spec":{"logging":{"max_size_mb":9,"max_files":9,"note":"` + progLeak + `"}}},
		{"name":"redis","kind":"image","command_display":"redis:7",
		 "spec":{"kind":"service","image":"redis:7","logging":{"max_size_mb":5,"max_files":2,"driver":"` + extraLeak + `"}},
		 "my_actions":["deploy"]},
		{"name":"nginx","kind":"image","command_display":"nginx:1",
		 "spec":{"kind":"service","image":"nginx:1"},
		 "my_actions":["deploy"]},
		{"name":"caddy","kind":"image","command_display":"caddy:1",
		 "spec":{"kind":"job","image":"caddy:1","logging":null},
		 "my_actions":["deploy"]},
		{"name":"edge","kind":"image","command_display":"edge:1",
		 "spec":{"kind":"service","image":"edge:1","logging":{"max_files":1,"max_size_mb":100}},
		 "my_actions":["deploy"]},
		{"name":"bad","kind":"image","command_display":"bad:1",
		 "spec":{"kind":"service","image":"bad:1","logging":{"max_size_mb":"` + strLeak + `","max_files":2}},
		 "my_actions":["deploy"]},
		{"name":"half","kind":"image","command_display":"half:1",
		 "spec":{"kind":"service","image":"half:1","logging":{"max_size_mb":5}},
		 "my_actions":["deploy"]},
		{"name":"frac","kind":"image","command_display":"frac:1",
		 "spec":{"kind":"service","image":"frac:1","logging":{"max_size_mb":10.5,"max_files":2}},
		 "my_actions":["deploy"]}
	]}`)
	out, err := projectCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, leak := range []string{progLeak, extraLeak, strLeak, "10.5"} {
		if strings.Contains(text, leak) {
			t.Fatalf("kept %s in %s", leak, text)
		}
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := dec.Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 8 {
		t.Fatalf("items %d", len(body.Items))
	}
	prog := body.Items[0]
	if len(prog) != 4 || prog["name"] != "tra-backend" || prog["kind"] != "program" {
		t.Fatalf("program %#v", prog)
	}
	if _, ok := prog["logging"]; ok {
		t.Fatalf("program logging %#v", prog["logging"])
	}
	redis := body.Items[1]
	log, _ := redis["logging"].(map[string]any)
	if len(log) != 2 || log["max_size_mb"] != json.Number("5") || log["max_files"] != json.Number("2") {
		t.Fatalf("redis logging %#v", redis["logging"])
	}
	if redis["healthcheck"] != false {
		t.Fatalf("healthcheck %#v", redis["healthcheck"])
	}
	for _, idx := range []int{2, 3, 5, 6, 7} {
		item := body.Items[idx]
		v, ok := item["logging"]
		if !ok || v != nil {
			t.Fatalf("%v logging %#v", item["name"], item["logging"])
		}
	}
	edge, _ := body.Items[4]["logging"].(map[string]any)
	if edge["max_size_mb"] != json.Number("100") || edge["max_files"] != json.Number("1") {
		t.Fatalf("edge %#v", edge)
	}

	one, err := projectOneCatalogEntry(json.RawMessage(`{"name":"redis","kind":"image","command_display":"redis:7","spec":{"kind":"service","image":"redis:7","logging":{"max_size_mb":5,"max_files":2,"driver":"` + extraLeak + `"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(one), extraLeak) {
		t.Fatalf("confirm kept extra %s", one)
	}
	var row map[string]any
	dec = json.NewDecoder(strings.NewReader(string(one)))
	dec.UseNumber()
	if err := dec.Decode(&row); err != nil {
		t.Fatal(err)
	}
	got, _ := row["logging"].(map[string]any)
	if got["max_size_mb"] != json.Number("5") || got["max_files"] != json.Number("2") || len(got) != 2 {
		t.Fatalf("confirm logging %#v", row["logging"])
	}
	progOne, err := projectOneCatalogEntry(json.RawMessage(`{"name":"tra-backend","kind":"program","command_display":"pnpm start","my_actions":["run"],"spec":{"logging":{"max_size_mb":9,"max_files":9,"note":"` + progLeak + `"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(progOne), progLeak) || strings.Contains(string(progOne), "logging") {
		t.Fatalf("program confirm %s", progOne)
	}
}
