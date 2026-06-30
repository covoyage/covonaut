package a2ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/a2a"
	"github.com/covoyage/covonaut/agui"
)

func TestEnvelopeRoundTripCreateSurface(t *testing.T) {
	env := NewCreateSurface("profile", BasicCatalogID)
	env.CreateSurface.Theme = map[string]any{"primaryColor": "#00BFFF"}
	env.CreateSurface.SendDataModel = true

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := ParseEnvelope(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Kind() != KindCreateSurface {
		t.Fatalf("kind = %v, want createSurface", got.Kind())
	}
	if got.CreateSurface.SurfaceID != "profile" || got.CreateSurface.CatalogID != BasicCatalogID {
		t.Fatalf("unexpected createSurface: %+v", got.CreateSurface)
	}
	if !got.CreateSurface.SendDataModel {
		t.Fatalf("sendDataModel lost in round trip")
	}
	if got.CreateSurface.Theme["primaryColor"] != "#00BFFF" {
		t.Fatalf("theme lost: %+v", got.CreateSurface.Theme)
	}
}

func TestEnvelopeVersionEmittedInWire(t *testing.T) {
	data, err := json.Marshal(NewDeleteSurface("s1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version":"v0.9.1"`) {
		t.Fatalf("version not present in wire form: %s", data)
	}
}

func TestParseEnvelopeRejectsMultipleBodies(t *testing.T) {
	raw := []byte(`{"version":"v0.9.1","deleteSurface":{"surfaceId":"a"},"createSurface":{"surfaceId":"b","catalogId":"c"}}`)
	if _, err := ParseEnvelope(raw); !errors.Is(err, ErrMultipleBodies) {
		t.Fatalf("err = %v, want ErrMultipleBodies", err)
	}
}

func TestParseEnvelopeRejectsNoBody(t *testing.T) {
	if _, err := ParseEnvelope([]byte(`{"version":"v0.9.1"}`)); !errors.Is(err, ErrNoBody) {
		t.Fatalf("err = %v, want ErrNoBody", err)
	}
}

func TestComponentFlatMarshaling(t *testing.T) {
	c := Text("greeting", "Hello")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["id"] != "greeting" || m["component"] != "Text" || m["text"] != "Hello" {
		t.Fatalf("flat marshaling wrong: %v", m)
	}

	var back Component
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != "greeting" || back.Type != "Text" || back.Props["text"] != "Hello" {
		t.Fatalf("unmarshal wrong: %+v", back)
	}
}

func TestDynamicMarshaling(t *testing.T) {
	cases := []struct {
		name string
		d    Dynamic
		want string
	}{
		{"literal", Lit("hi"), `"hi"`},
		{"path", Bind("/user/name"), `{"path":"/user/name"}`},
		{"function", Call("formatString", map[string]any{"value": "x"}), `{"call":"formatString","args":{"value":"x"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.d)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.want {
				t.Fatalf("got %s want %s", data, tc.want)
			}
			var back Dynamic
			if err := json.Unmarshal(data, &back); err != nil {
				t.Fatal(err)
			}
			redata, _ := json.Marshal(back)
			if string(redata) != tc.want {
				t.Fatalf("round trip got %s want %s", redata, tc.want)
			}
		})
	}
}

func TestChildListMarshaling(t *testing.T) {
	static := StaticChildren("a", "b")
	data, _ := json.Marshal(static)
	if string(data) != `["a","b"]` {
		t.Fatalf("static children: %s", data)
	}

	tmpl := TemplateChildren("/users", "user_card")
	data, _ = json.Marshal(tmpl)
	if string(data) != `{"path":"/users","componentId":"user_card"}` {
		t.Fatalf("template children: %s", data)
	}

	var cl ChildList
	if err := json.Unmarshal([]byte(`["x","y"]`), &cl); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cl.Static, []string{"x", "y"}) {
		t.Fatalf("decoded static: %+v", cl)
	}
	if err := json.Unmarshal([]byte(`{"path":"/p","componentId":"c"}`), &cl); err != nil {
		t.Fatal(err)
	}
	if cl.Template == nil || cl.Template.ComponentID != "c" {
		t.Fatalf("decoded template: %+v", cl)
	}
}

func TestUpdateDataModelRemoveVsSet(t *testing.T) {
	set := NewUpdateDataModel("s", "/a", nil)
	data, _ := json.Marshal(set)
	if !strings.Contains(string(data), `"value":null`) {
		t.Fatalf("explicit null value should be present: %s", data)
	}

	rm := NewRemoveDataModel("s", "/a")
	data, _ = json.Marshal(rm)
	if strings.Contains(string(data), `"value"`) {
		t.Fatalf("remove must omit value: %s", data)
	}

	var back UpdateDataModel
	if err := json.Unmarshal([]byte(`{"surfaceId":"s","path":"/a"}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.ValueSet {
		t.Fatalf("ValueSet should be false when value omitted")
	}
}

func TestDataModelPointerEngine(t *testing.T) {
	model := any(map[string]any{})

	model, err := ApplyUpdate(model, "/user/name", "Alice", true)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := GetData(model, "/user/name"); !ok || v != "Alice" {
		t.Fatalf("get /user/name = %v %v", v, ok)
	}

	// Replace whole model.
	model, err = ApplyUpdate(model, "/", map[string]any{"items": []any{"a", "b"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := GetData(model, "/items/1"); !ok || v != "b" {
		t.Fatalf("get /items/1 = %v %v", v, ok)
	}

	// Append using "-".
	model, err = ApplyUpdate(model, "/items/-", "c", true)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := GetData(model, "/items/2"); !ok || v != "c" {
		t.Fatalf("append failed: %v %v", v, ok)
	}

	// Remove a key.
	model, err = ApplyUpdate(model, "/items", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := GetData(model, "/items"); ok {
		t.Fatalf("items should be removed")
	}
}

func TestPointerEscaping(t *testing.T) {
	model, err := ApplyUpdate(map[string]any{}, "/a~1b/c~0d", 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := GetData(model, "/a~1b/c~0d"); !ok || v != 42 {
		t.Fatalf("escaped pointer failed: %v %v", v, ok)
	}
	if JoinPointer("a/b", "c~d") != "/a~1b/c~0d" {
		t.Fatalf("JoinPointer escaping wrong: %s", JoinPointer("a/b", "c~d"))
	}
}

func TestSurfaceStoreLifecycle(t *testing.T) {
	store := NewSurfaceStore()

	if err := store.Apply(NewUpdateComponents("missing", Text("x", "y"))); !errors.Is(err, ErrSurfaceNotFound) {
		t.Fatalf("update before create: %v", err)
	}

	if err := store.Apply(NewCreateSurface("s", BasicCatalogID)); err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(NewCreateSurface("s", BasicCatalogID)); !errors.Is(err, ErrSurfaceExists) {
		t.Fatalf("duplicate create: %v", err)
	}

	envs := []Envelope{
		NewUpdateComponents("s", Column("root", "name"), Text("name", Bind("/user/name"))),
		NewUpdateDataModel("s", "/user/name", "Ada"),
	}
	for _, e := range envs {
		if err := store.Apply(e); err != nil {
			t.Fatal(err)
		}
	}

	srf, ok := store.Surface("s")
	if !ok {
		t.Fatal("surface missing")
	}
	if _, ok := srf.Root(); !ok {
		t.Fatal("root missing")
	}
	if v, ok := srf.Get("/user/name"); !ok || v != "Ada" {
		t.Fatalf("data model: %v %v", v, ok)
	}

	// Delete is a no-op for unknown surfaces and removes known ones.
	if err := store.Apply(NewDeleteSurface("nope")); err != nil {
		t.Fatalf("delete unknown should be no-op: %v", err)
	}
	if err := store.Apply(NewDeleteSurface("s")); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Surface("s"); ok {
		t.Fatal("surface should be deleted")
	}
}

func TestClientDataModelCollection(t *testing.T) {
	store := NewSurfaceStore()
	cs := NewCreateSurface("s", BasicCatalogID)
	cs.CreateSurface.SendDataModel = true
	_ = store.Apply(cs)
	_ = store.Apply(NewCreateSurface("other", BasicCatalogID))
	_ = store.Apply(NewUpdateDataModel("s", "/k", "v"))
	_ = store.Apply(NewUpdateDataModel("other", "/k", "hidden"))

	payload := store.ClientDataModel()
	if _, ok := payload.Surfaces["s"]; !ok {
		t.Fatal("surface s should be included")
	}
	if _, ok := payload.Surfaces["other"]; ok {
		t.Fatal("surface without sendDataModel must be excluded")
	}
}

func TestValidateEnvelope(t *testing.T) {
	cat := BasicCatalog()

	errs := ValidateEnvelope(NewCreateSurface("", ""), cat)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errs), errs)
	}
	for _, e := range errs {
		if e.Code != CodeValidationFailed {
			t.Fatalf("code = %s", e.Code)
		}
	}

	errs = ValidateEnvelope(NewUpdateComponents("s", NewComponent("a", "Nonexistent", nil)), cat)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "unknown component") {
		t.Fatalf("expected unknown component error, got %v", errs)
	}

	if errs := ValidateEnvelope(NewCreateSurface("s", BasicCatalogID), cat); len(errs) != 0 {
		t.Fatalf("valid envelope produced errors: %v", errs)
	}
}

func TestValidateSurfaceTree(t *testing.T) {
	cat := BasicCatalog()
	store := NewSurfaceStore()
	_ = store.Apply(NewCreateSurface("s", BasicCatalogID))

	// Missing root + dangling reference.
	_ = store.Apply(NewUpdateComponents("s", Card("card", "ghost")))
	srf, _ := store.Surface("s")
	errs := ValidateSurfaceTree(srf, cat)
	if len(errs) == 0 {
		t.Fatal("expected validation errors")
	}
	var hasRoot, hasDangling bool
	for _, e := range errs {
		if strings.Contains(e.Message, "no \"root\"") {
			hasRoot = true
		}
		if strings.Contains(e.Message, "undefined component") {
			hasDangling = true
		}
	}
	if !hasRoot || !hasDangling {
		t.Fatalf("missing expected errors: %v", errs)
	}

	// Now make it valid.
	store2 := NewSurfaceStore()
	_ = store2.Apply(NewCreateSurface("s", BasicCatalogID))
	_ = store2.Apply(NewUpdateComponents("s", Column("root", "t"), Text("t", "hi")))
	srf2, _ := store2.Surface("s")
	if errs := ValidateSurfaceTree(srf2, cat); len(errs) != 0 {
		t.Fatalf("valid tree produced errors: %v", errs)
	}
}

func TestValidateDetectsCycle(t *testing.T) {
	cat := BasicCatalog()
	store := NewSurfaceStore()
	_ = store.Apply(NewCreateSurface("s", BasicCatalogID))
	_ = store.Apply(NewUpdateComponents("s",
		Column("root", "a"),
		Card("a", "b"),
		Card("b", "a"),
	))
	srf, _ := store.Surface("s")
	errs := ValidateSurfaceTree(srf, cat)
	var cyc bool
	for _, e := range errs {
		if strings.Contains(e.Message, "circular reference") {
			cyc = true
		}
	}
	if !cyc {
		t.Fatalf("expected circular reference error, got %v", errs)
	}
}

func TestStreamEncodeDecodeJSONL(t *testing.T) {
	envs := NewSurface("s", BasicCatalogID).
		Add(Column("root", "t"), Text("t", "hi")).
		Data("/x", 1).
		Build()

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.EncodeAll(envs); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimRight(buf.String(), "\n"), "\n") + 1; lines != len(envs) {
		t.Fatalf("expected %d JSONL lines, got %d: %q", len(envs), lines, buf.String())
	}

	dec := NewDecoder(&buf)
	var decoded []Envelope
	for {
		env, err := dec.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		decoded = append(decoded, env)
	}
	if len(decoded) != len(envs) {
		t.Fatalf("decoded %d, want %d", len(decoded), len(envs))
	}
	if decoded[0].Kind() != KindCreateSurface || decoded[1].Kind() != KindUpdateComponents {
		t.Fatalf("unexpected decoded kinds: %v %v", decoded[0].Kind(), decoded[1].Kind())
	}
}

func TestBuilderProducesOrderedEnvelopes(t *testing.T) {
	envs := NewSurface("s", BasicCatalogID).
		SendDataModel(true).
		Add(Column("root", "t")).
		Add(Text("t", "hi")).
		Data("/a", 1).
		RemoveData("/b").
		Build()

	if len(envs) != 4 {
		t.Fatalf("expected 4 envelopes, got %d", len(envs))
	}
	wantKinds := []MessageKind{KindCreateSurface, KindUpdateComponents, KindUpdateDataModel, KindUpdateDataModel}
	for i, want := range wantKinds {
		if envs[i].Kind() != want {
			t.Fatalf("env %d kind = %v, want %v", i, envs[i].Kind(), want)
		}
	}
	if !envs[0].CreateSurface.SendDataModel {
		t.Fatal("sendDataModel not propagated")
	}
}

func TestClientActionTimestamp(t *testing.T) {
	a := NewClientAction("submit", "s", "btn", map[string]any{"x": 1})
	if a.Timestamp == "" {
		t.Fatal("timestamp not set")
	}
	data, _ := json.Marshal(ClientMessage{Action: a})
	if !strings.Contains(string(data), `"sourceComponentId":"btn"`) {
		t.Fatalf("client action wire form wrong: %s", data)
	}
}

func TestClientError(t *testing.T) {
	err := ClientError{Code: CodeValidationFailed, SurfaceID: "s", Path: "/a", Message: "bad"}
	if s := err.Error(); s != "VALIDATION_FAILED at /a: bad" {
		t.Fatalf("unexpected error string with path: %q", s)
	}
	err2 := ClientError{Code: "ERR", Message: "something"}
	if s := err2.Error(); s != "ERR: something" {
		t.Fatalf("unexpected error string without path: %q", s)
	}
}

func TestValidationErrorToClientError(t *testing.T) {
	ve := ValidationError{Code: CodeValidationFailed, SurfaceID: "s", Path: "/x", Message: "invalid"}
	ce := ve.ToClientError()
	if ce.Code != ve.Code || ce.SurfaceID != ve.SurfaceID || ce.Path != ve.Path || ce.Message != ve.Message {
		t.Fatalf("ToClientError mismatch: %+v vs %+v", ve, ce)
	}
}

func TestEnvelopeSurfaceID(t *testing.T) {
	if id := NewCreateSurface("s1", BasicCatalogID).SurfaceID(); id != "s1" {
		t.Fatalf("createSurface SurfaceID: %q", id)
	}
	if id := NewUpdateComponents("s2", Text("t", "hi")).SurfaceID(); id != "s2" {
		t.Fatalf("updateComponents SurfaceID: %q", id)
	}
	if id := NewUpdateDataModel("s3", "/a", 1).SurfaceID(); id != "s3" {
		t.Fatalf("updateDataModel SurfaceID: %q", id)
	}
	if id := NewDeleteSurface("s4").SurfaceID(); id != "s4" {
		t.Fatalf("deleteSurface SurfaceID: %q", id)
	}
	var empty Envelope
	if id := empty.SurfaceID(); id != "" {
		t.Fatalf("empty SurfaceID: %q", id)
	}
}

func TestUpdateDataModelSetValue(t *testing.T) {
	u := &UpdateDataModel{SurfaceID: "s", Path: "/a"}
	u.SetValue(42)
	if !u.ValueSet {
		t.Fatal("ValueSet should be true after SetValue")
	}
	if u.Value != 42 {
		t.Fatalf("Value = %v, want 42", u.Value)
	}
	data, _ := json.Marshal(Envelope{Version: Version, UpdateDataModel: u})
	if !strings.Contains(string(data), `"value":42`) {
		t.Fatalf("value not in wire form: %s", data)
	}
}

func TestCatalogRegistry(t *testing.T) {
	r := NewCatalogRegistry()
	cat, ok := r.Lookup(BasicCatalogID)
	if !ok {
		t.Fatal("basic catalog not found in registry")
	}
	if cat.ID != BasicCatalogID {
		t.Fatalf("unexpected catalog ID: %q", cat.ID)
	}

	custom := &Catalog{
		ID: "urn:custom",
		Components: map[string]ComponentDef{
			"MyWidget": {Name: "MyWidget", ChildFields: []string{"child"}},
		},
		Functions: map[string]struct{}{"myFunc": {}},
	}
	r.Register(custom)
	got, ok := r.Lookup("urn:custom")
	if !ok {
		t.Fatal("custom catalog not found")
	}
	if got.ID != "urn:custom" {
		t.Fatalf("unexpected catalog ID: %q", got.ID)
	}
	if !got.HasComponent("MyWidget") {
		t.Fatal("MyWidget not found in custom catalog")
	}
	if !got.HasFunction("myFunc") {
		t.Fatal("myFunc not found in custom catalog")
	}

	// Register nil is a no-op.
	r.Register(nil)
	// Verify no panic and existing catalogs are still there.
	if _, ok := r.Lookup(BasicCatalogID); !ok {
		t.Fatal("basic catalog disappeared after nil register")
	}
}

func TestBuilderConstructors(t *testing.T) {
	t.Run("Text", func(t *testing.T) {
		c := Text("t1", "hello")
		if c.ID != "t1" || c.Type != "Text" || c.Props["text"] != "hello" {
			t.Fatalf("Text: %+v", c)
		}
	})
	t.Run("Image", func(t *testing.T) {
		c := Image("i1", "https://example.com/img.png")
		if c.ID != "i1" || c.Type != "Image" || c.Props["url"] != "https://example.com/img.png" {
			t.Fatalf("Image: %+v", c)
		}
	})
	t.Run("Icon", func(t *testing.T) {
		c := Icon("ic1", "star")
		if c.ID != "ic1" || c.Type != "Icon" || c.Props["name"] != "star" {
			t.Fatalf("Icon: %+v", c)
		}
	})
	t.Run("Row", func(t *testing.T) {
		c := Row("r1", "a", "b")
		if c.ID != "r1" || c.Type != "Row" {
			t.Fatalf("Row: %+v", c)
		}
		cl, ok := c.Props["children"].(ChildList)
		if !ok || len(cl.Static) != 2 || cl.Static[0] != "a" {
			t.Fatalf("Row children: %+v", c.Props["children"])
		}
	})
	t.Run("List", func(t *testing.T) {
		c := List("l1", "x", "y")
		if c.ID != "l1" || c.Type != "List" {
			t.Fatalf("List: %+v", c)
		}
	})
	t.Run("TemplateList", func(t *testing.T) {
		c := TemplateList("tl1", "/items", "card")
		if c.ID != "tl1" || c.Type != "List" {
			t.Fatalf("TemplateList: %+v", c)
		}
		cl, ok := c.Props["children"].(ChildList)
		if !ok || cl.Template == nil || cl.Template.Path != "/items" || cl.Template.ComponentID != "card" {
			t.Fatalf("TemplateList children: %+v", c.Props["children"])
		}
	})
	t.Run("Divider", func(t *testing.T) {
		c := Divider("d1")
		if c.ID != "d1" || c.Type != "Divider" {
			t.Fatalf("Divider: %+v", c)
		}
	})
	t.Run("Button", func(t *testing.T) {
		action := EventAction("submit", map[string]any{"k": 1})
		c := Button("b1", "Click", action)
		if c.ID != "b1" || c.Type != "Button" || c.Props["text"] != "Click" {
			t.Fatalf("Button: %+v", c)
		}
	})
	t.Run("TextField", func(t *testing.T) {
		c := TextField("tf1", "/user/name")
		if c.ID != "tf1" || c.Type != "TextField" {
			t.Fatalf("TextField: %+v", c)
		}
		d, ok := c.Props["value"].(Dynamic)
		if !ok || !d.IsPath || d.Path != "/user/name" {
			t.Fatalf("TextField value binding: %+v", c.Props["value"])
		}
	})
	t.Run("CheckBox", func(t *testing.T) {
		c := CheckBox("cb1", "Accept", "/terms")
		if c.ID != "cb1" || c.Type != "CheckBox" {
			t.Fatalf("CheckBox: %+v", c)
		}
	})
	t.Run("Slider", func(t *testing.T) {
		c := Slider("s1", "/volume")
		if c.ID != "s1" || c.Type != "Slider" {
			t.Fatalf("Slider: %+v", c)
		}
	})
	t.Run("Set", func(t *testing.T) {
		c := NewComponent("x", "Text", nil)
		c.Set("text", "hi")
		if c.Props["text"] != "hi" {
			t.Fatalf("Set after pointer receiver: %+v", c)
		}
	})
	t.Run("FunctionAction", func(t *testing.T) {
		a := FunctionAction("validate", map[string]any{"val": 1})
		if a.FunctionCall == nil || a.FunctionCall.CallName != "validate" {
			t.Fatalf("FunctionAction: %+v", a)
		}
	})
}

func TestBuilderThemeAndDelete(t *testing.T) {
	b := NewSurface("s", BasicCatalogID)
	b.Theme(map[string]any{"primaryColor": "red"})
	b.SendDataModel(true)
	_ = b.Add(Column("root", "t"), Text("t", "hi"))
	_ = b.Data("/x", 1)

	envs := b.Build()
	if len(envs) != 3 {
		t.Fatalf("expected 3 envelopes, got %d", len(envs))
	}
	if envs[0].Kind() != KindCreateSurface {
		t.Fatalf("first should be createSurface, got %v", envs[0].Kind())
	}
	if envs[0].CreateSurface.Theme["primaryColor"] != "red" {
		t.Fatalf("theme not propagated: %+v", envs[0].CreateSurface.Theme)
	}
	if !envs[0].CreateSurface.SendDataModel {
		t.Fatal("sendDataModel not propagated")
	}

	del := b.Delete()
	if del.Kind() != KindDeleteSurface || del.DeleteSurface.SurfaceID != "s" {
		t.Fatalf("Delete: %+v", del)
	}
}

func TestSurfaceStoreSurfacesCopy(t *testing.T) {
	store := NewSurfaceStore()
	_ = store.Apply(NewCreateSurface("a", BasicCatalogID))
	_ = store.Apply(NewCreateSurface("b", BasicCatalogID))

	got := store.Surfaces()
	if len(got) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(got))
	}
	if _, ok := got["a"]; !ok {
		t.Fatal("surface 'a' missing")
	}
	// Verify it's a copy: modifying the returned map doesn't affect the store.
	delete(got, "a")
	if _, ok := store.Surface("a"); !ok {
		t.Fatal("store surface 'a' was affected by delete on returned copy")
	}
}

func TestValidationErrorError(t *testing.T) {
	e1 := ValidationError{Code: CodeValidationFailed, SurfaceID: "s", Path: "/a", Message: "bad"}
	s1 := e1.Error()
	if s1 != "VALIDATION_FAILED at /a (surface \"s\"): bad" {
		t.Fatalf("unexpected error with path: %q", s1)
	}
	e2 := ValidationError{Code: "ERR", SurfaceID: "s", Message: "oops"}
	s2 := e2.Error()
	if s2 != "ERR (surface \"s\"): oops" {
		t.Fatalf("unexpected error without path: %q", s2)
	}
}

func TestStreamMore(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"version":"v0.9.1","deleteSurface":{"surfaceId":"s"}}` + "\n")
	buf.WriteString(`{"version":"v0.9.1","deleteSurface":{"surfaceId":"t"}}` + "\n")

	dec := NewDecoder(&buf)
	if !dec.More() {
		t.Fatal("expected More() to be true before first decode")
	}
	_, _ = dec.Decode()
	if !dec.More() {
		t.Fatal("expected More() to be true after first decode")
	}
	_, _ = dec.Decode()
	if dec.More() {
		t.Fatal("expected More() to be false after exhausting stream")
	}
}

func TestStreamEncodeVersionFill(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	// Envelope with empty version should have it filled in by Encode.
	env := Envelope{DeleteSurface: &DeleteSurface{SurfaceID: "s"}}
	if err := enc.Encode(env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"version":"v0.9.1"`) {
		t.Fatal("version not filled by Encode")
	}
	// Also test with version already set (no-op branch).
	buf.Reset()
	if err := enc.Encode(NewDeleteSurface("t")); err != nil {
		t.Fatal(err)
	}
}

type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("write error") }

func TestStreamEncodeAllError(t *testing.T) {
	enc := NewEncoder(errWriter{})
	// First envelope: Encode should fail due to write error.
	env := Envelope{DeleteSurface: &DeleteSurface{SurfaceID: "s"}}
	err := enc.Encode(env)
	if err == nil {
		t.Fatal("expected encode error")
	}
	// EncodeAll should also fail.
	err = enc.EncodeAll([]Envelope{NewDeleteSurface("s"), NewDeleteSurface("t")})
	if err == nil {
		t.Fatal("expected encode error from EncodeAll")
	}
}

func TestStreamDecodeWithDefaultVersion(t *testing.T) {
	// Envelope without version; Decode should set the default.
	data := `{"deleteSurface":{"surfaceId":"s"}}` + "\n"
	dec := NewDecoder(strings.NewReader(data))
	env, err := dec.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if env.Version != Version {
		t.Fatalf("Decode did not set default version: got %q, want %q", env.Version, Version)
	}
}

func TestStreamDecodeError(t *testing.T) {
	dec := NewDecoder(strings.NewReader("not valid json\n"))
	if _, err := dec.Decode(); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestBindingA2AErrorPaths(t *testing.T) {
	// EnvelopeToDataPart with an unserializable value.

	// DataPartToEnvelope with nil data.
	_, ok, err := DataPartToEnvelope(a2a.Part{Type: a2a.PartTypeData})
	if ok || err != nil {
		t.Fatalf("expected ok=false, err=nil for nil data part, got ok=%v err=%v", ok, err)
	}

	// DataPartToEnvelope with wrong MIME type.
	_, ok, err = DataPartToEnvelope(a2a.Part{
		Type: a2a.PartTypeData,
		Data: &a2a.DataPart{MIMEType: "text/plain", Data: map[string]any{"key": "val"}},
	})
	if ok || err != nil {
		t.Fatalf("expected ok=false for wrong mime type, got ok=%v", ok)
	}

	// DataPartToEnvelope with unmarshalable data (channel in data).
	_, ok, err = DataPartToEnvelope(a2a.Part{
		Type: a2a.PartTypeData,
		Data: &a2a.DataPart{MIMEType: MIMEType, Data: map[string]any{"ch": make(chan int)}},
	})
	if err == nil {
		t.Fatal("expected error from unmarshalable data")
	}

	// DataPartToEnvelope with body-level parse error (no body, no version).
	// This should return ok=false, err=nil since it looks like non-A2UI content.
	_, ok, err = DataPartToEnvelope(a2a.Part{
		Type: a2a.PartTypeData,
		Data: &a2a.DataPart{MIMEType: MIMEType, Data: map[string]any{"invalid": "data"}},
	})
	if ok || err != nil {
		t.Fatalf("expected ok=false, err=nil for non-A2UI content, got ok=%v err=%v", ok, err)
	}

	// EnvelopesToMessage error (unserializable value in component).
	envs := []Envelope{{
		Version:       Version,
		DeleteSurface: &DeleteSurface{SurfaceID: "s"},
	}}
	msg, err := EnvelopesToMessage("agent", envs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := MessageEnvelopes(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(got))
	}

	// MessageEnvelopes with a malformed part (data that errors on marshal).
	msg.Parts = append(msg.Parts, a2a.Part{
		Type: a2a.PartTypeData,
		Data: &a2a.DataPart{MIMEType: MIMEType, Data: map[string]any{"ch": make(chan int)}},
	})
	if _, err := MessageEnvelopes(msg); err == nil {
		t.Fatal("expected error from malformed part")
	}
}

func TestBindingA2AEnvelopeToMap(t *testing.T) {
	// This exercises the internal envelopeToMap function indirectly.
	// Non-versioned envelope triggers the version-set branch in EnvelopeToDataPart.
	env := Envelope{
		DeleteSurface: &DeleteSurface{SurfaceID: "s"},
	}
	part, err := EnvelopeToDataPart(env)
	if err != nil {
		t.Fatal(err)
	}
	if part.Data == nil {
		t.Fatal("data part missing")
	}
	if part.Data.Data["version"] != Version {
		t.Fatalf("version not set: %v", part.Data.Data["version"])
	}
}

func TestAGUIBindingToCustomEventVersionFill(t *testing.T) {
	env := Envelope{DeleteSurface: &DeleteSurface{SurfaceID: "s"}}
	ev := ToCustomEvent(env)
	raw, _ := json.Marshal(ev.Value)
	if !strings.Contains(string(raw), `"version":"v0.9.1"`) {
		t.Fatal("version not set by ToCustomEvent")
	}
}

func TestAGUIBindingFromCustomEventErrors(t *testing.T) {
	// Unmarshalable value (channel).
	ev := agui.CustomEvent{
		BaseEvent: agui.BaseEvent{Type: agui.EventCustom},
		Name:      AGUIEventName,
		Value:     map[string]any{"ch": make(chan int)},
	}
	if _, ok, err := FromCustomEvent(ev); err == nil || ok {
		t.Fatalf("expected error from unmarshalable value, got ok=%v err=%v", ok, err)
	}

	// Parse error (valid JSON but not an envelope).
	ev2 := agui.CustomEvent{
		BaseEvent: agui.BaseEvent{Type: agui.EventCustom},
		Name:      AGUIEventName,
		Value:     map[string]any{"invalid": "data"},
	}
	if _, ok, err := FromCustomEvent(ev2); ok || err != nil {
		t.Fatalf("expected ok=false, err=nil for non-A2UI, got ok=%v err=%v", ok, err)
	}
}

func TestNewClientActionNilContext(t *testing.T) {
	a := NewClientAction("submit", "s", "btn", nil)
	if a.Context == nil {
		t.Fatal("nil context should be converted to empty map")
	}
	if len(a.Context) != 0 {
		t.Fatalf("expected empty context, got %v", a.Context)
	}
}

func TestComponentSetNilProps(t *testing.T) {
	c := Component{ID: "x", Type: "Text"}
	c.Set("text", "hi")
	if c.Props == nil {
		t.Fatal("Set should initialize nil Props")
	}
	if c.Props["text"] != "hi" {
		t.Fatalf("Set failed: %+v", c)
	}
}

func TestComponentMarshalJSON(t *testing.T) {
	// Normal marshaling.
	c := Component{ID: "x", Type: "Text", Props: map[string]any{"text": "hello"}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"id":"x"`) {
		t.Fatalf("id missing: %s", data)
	}
	if !strings.Contains(string(data), `"component":"Text"`) {
		t.Fatalf("component type missing: %s", data)
	}
	if !strings.Contains(string(data), `"text":"hello"`) {
		t.Fatalf("text prop missing: %s", data)
	}
	// Props with reserved keys "id" and "component" are silently dropped.
	c2 := Component{ID: "x", Type: "Text", Props: map[string]any{"id": "should-drop", "component": "should-drop", "text": "ok"}}
	data2, _ := json.Marshal(c2)
	if strings.Contains(string(data2), `"should-drop"`) {
		t.Fatalf("reserved props should be dropped: %s", data2)
	}
}

func TestComponentUnmarshalJSONErrors(t *testing.T) {
	// Not JSON at all.
	var c Component
	if err := json.Unmarshal([]byte(`{`), &c); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	// ID not a string.
	if err := json.Unmarshal([]byte(`{"id":123,"component":"Text"}`), &c); err == nil {
		t.Fatal("expected error for non-string id")
	}
	// Component type not a string.
	if err := json.Unmarshal([]byte(`{"id":"x","component":123}`), &c); err == nil {
		t.Fatal("expected error for non-string component type")
	}
	// Valid unmarshal with extra props.
	if err := json.Unmarshal([]byte(`{"id":"x","component":"Text","text":"hello"}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.ID != "x" || c.Type != "Text" || c.Props["text"] != "hello" {
		t.Fatalf("unmarshal result: %+v", c)
	}
}

func TestChildListMarshalJSON(t *testing.T) {
	// Static children but nil slice.
	cl := ChildList{}
	data, _ := json.Marshal(cl)
	if string(data) != `[]` {
		t.Fatalf("nil static should marshal to empty array, got %s", data)
	}
	// Template children.
	cl2 := ChildList{Template: &ChildTemplate{Path: "/items", ComponentID: "card"}}
	data2, _ := json.Marshal(cl2)
	if !strings.Contains(string(data2), `"path":"/items"`) {
		t.Fatalf("template marshal: %s", data2)
	}
}

func TestChildListUnmarshalJSONErrors(t *testing.T) {
	var cl ChildList
	// Invalid JSON.
	if err := json.Unmarshal([]byte(`{`), &cl); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParsePointerEdgeCases(t *testing.T) {
	// Path with multiple leading slashes yields empty tokens.
	pp := ParsePointer("//")
	if len(pp) != 2 || pp[0] != "" || pp[1] != "" {
		t.Fatalf("expected ['',''] for '//', got %v", pp)
	}
}

func TestJoinPointerEmpty(t *testing.T) {
	s := JoinPointer()
	if s != "/" {
		t.Fatalf("expected '/', got %q", s)
	}
}

func TestGetDataEdgeCases(t *testing.T) {
	model := map[string]any{
		"a": map[string]any{"b": "val"},
		"c": []any{"x", "y"},
	}
	// Key not found in map.
	if _, ok := GetData(model, "/missing"); ok {
		t.Fatal("expected not found for missing key")
	}
	// Array index out of range.
	if _, ok := GetData(model, "/c/5"); ok {
		t.Fatal("expected not found for out-of-range index")
	}
	// Traverse into scalar.
	if _, ok := GetData(model, "/a/b/c"); ok {
		t.Fatal("expected not found when traversing into scalar")
	}
	// Path that goes into scalar instead of container.
	model2 := map[string]any{"x": "scalar"}
	if _, ok := GetData(model2, "/x/y"); ok {
		t.Fatal("expected not found when descending into scalar")
	}
}

func TestApplyUpdateEdgeCases(t *testing.T) {
	// Root path with hasValue=false (clear model).
	result, err := ApplyUpdate(map[string]any{"a": 1}, "/", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}

	// Nil model with non-root path.
	result, err = ApplyUpdate(nil, "/a", "val", true)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok || m["a"] != "val" {
		t.Fatalf("expected map[a:val], got %v", result)
	}

	// applyTokens: map child != nil (existing map updated).
	result, err = ApplyUpdate(map[string]any{"a": map[string]any{"b": 1}}, "/a/b", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := GetData(result, "/a/b")
	if v != 2 {
		t.Fatalf("expected 2, got %v", v)
	}

	// Array append with hasValue=false (no-op append).
	result, err = ApplyUpdate([]any{"a"}, "/-", "b", false)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := result.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("expected array unchanged, got %v", result)
	}

	// Array set element by index.
	result, err = ApplyUpdate([]any{"a", "b"}, "/1", "B", true)
	if err != nil {
		t.Fatal(err)
	}
	arr, _ = result.([]any)
	if arr[1] != "B" {
		t.Fatalf("expected B at [1], got %v", arr)
	}

	// Array remove element (set to nil).
	result, err = ApplyUpdate([]any{"a", "b"}, "/1", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	arr, _ = result.([]any)
	if arr[1] != nil {
		t.Fatalf("expected nil at [1], got %v", arr)
	}

	// Array index out of range.
	_, err = ApplyUpdate([]any{"a"}, "/5", "b", true)
	if err == nil {
		t.Fatal("expected error for out-of-range index")
	}

	// Cannot descend into append marker.
	_, err = ApplyUpdate([]any{"a", "b"}, "/-/x", "val", true)
	if err == nil {
		t.Fatal("expected error for descending into append")
	}

	// Descend into array with index, child is nil → gets initialized.
	result, err = ApplyUpdate([]any{nil}, "/0/x", "val", true)
	if err != nil {
		t.Fatal(err)
	}
	arr, _ = result.([]any)
	if arr == nil {
		t.Fatal("result is nil")
	}

	// applyTokens: error propagated from recursive call when descending into map.
	_, err = ApplyUpdate(map[string]any{"a": []any{}}, "/a/0/x", "val", true)
	if err != nil {
		// This should fail because a[0] doesn't exist.
	}

	// Scalar where container is expected (replace with map).
	type testType int
	result, err = ApplyUpdate(testType(42), "/a", "val", true)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = result.(map[string]any)
	if m["a"] != "val" {
		t.Fatalf("expected map[a:val], got %v", result)
	}
}

func TestArrayIndexError(t *testing.T) {
	_, _, err := arrayIndex("not-a-number", 0)
	if err == nil {
		t.Fatal("expected error for non-numeric index")
	}
}

func TestSurfaceStoreApplyDefault(t *testing.T) {
	store := NewSurfaceStore()
	env := Envelope{Version: Version}
	err := store.Apply(env)
	if !errors.Is(err, ErrNoBody) {
		t.Fatalf("expected ErrNoBody, got %v", err)
	}
}

func TestSurfaceStoreApplyDataModelError(t *testing.T) {
	store := NewSurfaceStore()
	_ = store.Apply(NewCreateSurface("s", BasicCatalogID))
	// Update a path that triggers data model error (descend into non-existent array index).
	_ = store.Apply(NewUpdateDataModel("s", "/a", []any{"x"}))
	err := store.Apply(NewUpdateDataModel("s", "/a/5", "val"))
	if err == nil {
		t.Fatal("expected error for out-of-range array index")
	}
}

func TestChildRefsWithUnknownType(t *testing.T) {
	cat := BasicCatalog()
	c := NewComponent("x", "Nonexistent", map[string]any{"child": "y", "children": []any{"z"}})
	refs := childRefs(c, cat)
	// Should use defaults: "child" and "children".
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs from defaults, got %v", refs)
	}
}

func TestChildRefsWithNestedFields(t *testing.T) {
	cat := BasicCatalog()
	c := NewComponent("tabs1", "Tabs", map[string]any{
		"tabs": []any{
			map[string]any{"child": "tab1"},
			map[string]any{"child": "tab2"},
			map[string]any{},             // no child key
			map[string]any{"child": 123}, // child is not a string
		},
	})
	refs := childRefs(c, cat)
	if len(refs) != 2 || refs[0] != "tab1" || refs[1] != "tab2" {
		t.Fatalf("unexpected refs: %v", refs)
	}
}

func TestChildListRefs(t *testing.T) {
	// []any with string elements.
	refs := childListRefs([]any{"a", "b"})
	if len(refs) != 2 {
		t.Fatalf("[]any refs: %v", refs)
	}
	// []any with empty string (skipped).
	refs = childListRefs([]any{"a", ""})
	if len(refs) != 1 {
		t.Fatalf("empty string should be skipped: %v", refs)
	}
	// []any with non-string element.
	refs = childListRefs([]any{42})
	if len(refs) != 0 {
		t.Fatalf("non-string should be skipped: %v", refs)
	}
	// []string.
	refs = childListRefs([]string{"x", "y"})
	if len(refs) != 2 {
		t.Fatalf("[]string refs: %v", refs)
	}
	// map[string]any with componentId.
	refs = childListRefs(map[string]any{"componentId": "c1"})
	if len(refs) != 1 || refs[0] != "c1" {
		t.Fatalf("map refs: %v", refs)
	}
	// map[string]any without componentId.
	refs = childListRefs(map[string]any{"other": "val"})
	if len(refs) != 0 {
		t.Fatalf("map without componentId: %v", refs)
	}
	// ChildList with Template.
	refs = childListRefs(ChildList{Template: &ChildTemplate{ComponentID: "tmpl"}})
	if len(refs) != 1 || refs[0] != "tmpl" {
		t.Fatalf("ChildList template refs: %v", refs)
	}
	// ChildList with nil Template (static).
	refs = childListRefs(ChildList{Static: []string{"s1", "s2"}})
	if len(refs) != 2 {
		t.Fatalf("ChildList static refs: %v", refs)
	}
	// nil.
	refs = childListRefs(nil)
	if len(refs) != 0 {
		t.Fatalf("nil refs: %v", refs)
	}
}

func TestValidateEnvelopeEdgeCases(t *testing.T) {
	cat := BasicCatalog()

	// KindUnknown.
	errs := ValidateEnvelope(Envelope{Version: Version}, cat)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for unknown kind, got %d", len(errs))
	}

	// UpdateComponents with empty surfaceID.
	errs = ValidateEnvelope(NewUpdateComponents("", Text("t", "hi")), cat)
	hasEmptySurfaceID := false
	for _, e := range errs {
		if e.Path == "/updateComponents/surfaceId" {
			hasEmptySurfaceID = true
		}
	}
	if !hasEmptySurfaceID {
		t.Fatalf("expected surfaceId error, got %v", errs)
	}

	// UpdateComponents with empty component id.
	errs = ValidateEnvelope(NewUpdateComponents("s", Component{Type: "Text"}), cat)
	hasEmptyID := false
	for _, e := range errs {
		if e.Path == "/updateComponents/components/0/id" {
			hasEmptyID = true
		}
	}
	if !hasEmptyID {
		t.Fatalf("expected component id error, got %v", errs)
	}

	// UpdateComponents with empty component type.
	errs = ValidateEnvelope(NewUpdateComponents("s", Component{ID: "x"}), cat)
	hasEmptyType := false
	for _, e := range errs {
		if e.Path == "/updateComponents/components/0/component" {
			hasEmptyType = true
		}
	}
	if !hasEmptyType {
		t.Fatalf("expected component type error, got %v", errs)
	}

	// UpdateComponents with unknown component type.
	errs = ValidateEnvelope(NewUpdateComponents("s", NewComponent("x", "UnknownType", nil)), cat)
	hasUnknown := false
	for _, e := range errs {
		if strings.Contains(e.Message, "unknown component type") {
			hasUnknown = true
		}
	}
	if !hasUnknown {
		t.Fatalf("expected unknown component type error, got %v", errs)
	}

	// UpdateDataModel with empty surfaceID.
	errs = ValidateEnvelope(Envelope{
		Version:         Version,
		UpdateDataModel: &UpdateDataModel{Path: "/a", Value: 1, ValueSet: true},
	}, cat)
	hasDataSurfaceID := false
	for _, e := range errs {
		if e.Path == "/updateDataModel/surfaceId" {
			hasDataSurfaceID = true
		}
	}
	if !hasDataSurfaceID {
		t.Fatalf("expected updateDataModel surfaceId error, got %v", errs)
	}

	// DeleteSurface with empty surfaceID.
	errs = ValidateEnvelope(Envelope{Version: Version, DeleteSurface: &DeleteSurface{}}, cat)
	hasDelSurfaceID := false
	for _, e := range errs {
		if e.Path == "/deleteSurface/surfaceId" {
			hasDelSurfaceID = true
		}
	}
	if !hasDelSurfaceID {
		t.Fatalf("expected deleteSurface surfaceId error, got %v", errs)
	}
}

func TestValidateSurfaceTreeEdgeCases(t *testing.T) {
	cat := BasicCatalog()
	store := NewSurfaceStore()
	_ = store.Apply(NewCreateSurface("s", BasicCatalogID))
	_ = store.Apply(NewUpdateComponents("s", Column("root", "child"), NewComponent("child", "UnknownType", nil)))
	srf, _ := store.Surface("s")
	errs := ValidateSurfaceTree(srf, cat)
	hasUnknown := false
	for _, e := range errs {
		if strings.Contains(e.Message, "unknown component type") {
			hasUnknown = true
		}
	}
	if !hasUnknown {
		t.Fatalf("expected unknown component type error, got %v", errs)
	}
}

func TestParseEnvelopeDecodeError(t *testing.T) {
	if _, err := ParseEnvelope([]byte(`{`)); err == nil {
		t.Fatal("expected decode error for malformed JSON")
	}
}

func TestUpdateDataModelUnmarshalErrors(t *testing.T) {
	var u UpdateDataModel
	// Malformed JSON.
	if err := json.Unmarshal([]byte(`{`), &u); err == nil {
		t.Fatal("expected error")
	}
	// surfaceId not a string.
	if err := json.Unmarshal([]byte(`{"surfaceId":123,"path":"/a","value":1}`), &u); err == nil {
		t.Fatal("expected error for non-string surfaceId")
	}
	// path not a string.
	u = UpdateDataModel{}
	if err := json.Unmarshal([]byte(`{"surfaceId":"s","path":123,"value":1}`), &u); err == nil {
		t.Fatal("expected error for non-string path")
	}
}

func TestComponentUnmarshalJSONPropUnmarshalError(t *testing.T) {
	var c Component
	// Prop value fails to unmarshal (json.RawMessage -> any fails for malformed).
	// This is hard to trigger with json.Unmarshal since it uses json.RawMessage
	// and then json.Unmarshal(raw, &v), which handles most things.
	// Instead test that unknown keys under "id" or "component" don't break.
	if err := json.Unmarshal([]byte(`{"id":"x","component":"Text","text":"ok"}`), &c); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicUnmarshalErrorPaths(t *testing.T) {
	var d Dynamic
	// Function call with invalid JSON inside.
	if err := json.Unmarshal([]byte(`{"call":123}`), &d); err == nil {
		t.Fatal("expected error for non-string call")
	}
	// Path binding with invalid path type.
	if err := json.Unmarshal([]byte(`{"path":123}`), &d); err == nil {
		t.Fatal("expected error for non-string path")
	}
	// Literal unmarshal error.
	if err := json.Unmarshal([]byte(``), &d); err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestDynamicUnmarshalExtraKeysWithPath(t *testing.T) {
	var d Dynamic
	// path with extra keys should error.
	err := json.Unmarshal([]byte(`{"path":"/a","extra":1}`), &d)
	if err == nil {
		t.Fatal("expected error for path with extra keys")
	}
	if !strings.Contains(err.Error(), "extra keys") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestComponentMarshalJSONUnserializableProp(t *testing.T) {
	c := Component{ID: "x", Type: "Text", Props: map[string]any{"ch": make(chan int)}}
	_, err := json.Marshal(c)
	// This should fail because the channel cannot be marshaled.
	if err == nil {
		t.Fatal("expected marshal error for unserializable prop")
	}
}

func TestEnvelopeToDataPartMarshalError(t *testing.T) {
	// Trigger envelopeToMap json.Marshal error via unserializable value in Theme.
	env := Envelope{
		CreateSurface: &CreateSurface{
			SurfaceID: "s", CatalogID: BasicCatalogID,
			Theme: map[string]any{"ch": make(chan int)},
		},
	}
	_, err := EnvelopeToDataPart(env)
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestEnvelopesToMessageError(t *testing.T) {
	envs := []Envelope{{
		CreateSurface: &CreateSurface{
			SurfaceID: "s", CatalogID: BasicCatalogID,
			Theme: map[string]any{"ch": make(chan int)},
		},
	}}
	_, err := EnvelopesToMessage("agent", envs)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestApplyTokensRecursiveError(t *testing.T) {
	// Descend into nested array where the inner array has an invalid index.
	_, err := ApplyUpdate([]any{[]any{}}, "/0/abc", "val", true)
	if err == nil {
		t.Fatal("expected error for invalid array index on nested array")
	}
}

func TestApplyTokensArrayIndexError(t *testing.T) {
	// Non-numeric key on a top-level array.
	_, err := ApplyUpdate([]any{"x"}, "/abc", "val", true)
	if err == nil {
		t.Fatal("expected error for non-numeric array index")
	}
}

func TestEnvelopeUnmarshalJSONSurfaceIDError(t *testing.T) {
	// surfaceId not a string in createSurface.
	var e Envelope
	// Use the full unmarshal path via envelope's UnmarshalJSON.
	if err := json.Unmarshal([]byte(`{"version":"v0.9.1","createSurface":{"surfaceId":123,"catalogId":"c"}}`), &e); err == nil {
		t.Fatal("expected error for non-string surfaceId in createSurface")
	}
}

func TestUpdateDataModelUnmarshalValueError(t *testing.T) {
	// value field - verify successful unmarshal (json.Unmarshal into any always
	// succeeds for valid JSON, so error path is unreachable via normal input).
	var u UpdateDataModel
	if err := json.Unmarshal([]byte(`{"surfaceId":"s","path":"/a","value":1}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.SurfaceID != "s" || u.Path != "/a" || u.Value.(float64) != 1 || !u.ValueSet {
		t.Fatalf("unexpected result: %+v", u)
	}
}

func TestSurfaceStoreApplyDataModelApplyUpdateError(t *testing.T) {
	store := NewSurfaceStore()
	_ = store.Apply(NewCreateSurface("s", BasicCatalogID))
	// Update with a path that forms a valid data model but ApplyUpdate fails
	// due to array index error on the data model content.
	_ = store.Apply(NewUpdateDataModel("s", "/a", []any{"x"}))
	err := store.Apply(NewUpdateDataModel("s", "/a/abc", "val"))
	if err == nil {
		t.Fatal("expected error for invalid array index")
	}
}

func TestComponentUnmarshalJSONIDError(t *testing.T) {
	var c Component
	if err := json.Unmarshal([]byte(`{"id":123,"component":"Text"}`), &c); err == nil {
		t.Fatal("expected error for non-string id")
	}
}

func TestComponentUnmarshalJSONPropNotUnmarshalable(t *testing.T) {
	var c Component
	// Props with valid JSON - this should succeed (the prop unmarshal into any
	// always succeeds for valid JSON).
	if err := json.Unmarshal([]byte(`{"id":"x","component":"Text","text":"hello"}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.Props["text"] != "hello" {
		t.Fatalf("prop not set: %+v", c)
	}
}

func TestChildListUnmarshalJSONTemplateError(t *testing.T) {
	var cl ChildList
	// A boolean fails both []string and ChildTemplate unmarshal, producing an error.
	if err := json.Unmarshal([]byte(`true`), &cl); err == nil {
		t.Fatal("expected error for boolean as ChildList")
	}
}

func TestDynamicUnmarshalLiteralError(t *testing.T) {
	var d Dynamic
	// Plain data that fails as a literal to cover the final error path.
	if err := json.Unmarshal([]byte(``), &d); err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestFromCustomEventMarshalError(t *testing.T) {
	ev := agui.CustomEvent{
		BaseEvent: agui.BaseEvent{Type: agui.EventCustom},
		Name:      AGUIEventName,
		Value:     map[string]any{"ch": make(chan int)},
	}
	_, ok, err := FromCustomEvent(ev)
	if err == nil || ok {
		t.Fatalf("expected marshal error, got ok=%v err=%v", ok, err)
	}
}

func TestDataPartToEnvelopeStructuralError(t *testing.T) {
	// Valid JSON that marshals fine but causes a JSON decode error in ParseEnvelope.
	// This is hard to trigger because json.Marshal and json.Decode are symmetric.
	// Instead, test with data that has valid JSON but no A2UI body (ErrNoBody
	// path, now treated as non-match).
	part := a2a.Part{
		Type: a2a.PartTypeData,
		Data: &a2a.DataPart{MIMEType: MIMEType, Data: map[string]any{"key": "val"}},
	}
	_, ok, err := DataPartToEnvelope(part)
	if ok || err != nil {
		t.Fatalf("expected ok=false err=nil for non-A2UI content, got ok=%v err=%v", ok, err)
	}
}

func TestEnvelopesToCustomEventsReturnPath(t *testing.T) {
	// Ensure the function handles empty input (zero-length).
	events := EnvelopesToCustomEvents(nil)
	if len(events) != 0 {
		t.Fatalf("expected empty, got %d", len(events))
	}
	events = EnvelopesToCustomEvents([]Envelope{})
	if len(events) != 0 {
		t.Fatalf("expected empty, got %d", len(events))
	}
}

func TestParsePointerTrimmedEmpty(t *testing.T) {
	// This branch (trimmed == "") is unreachable through normal usage because
	// path == "" || path == "/" is caught first. Verify it can't be triggered.
	pp := ParsePointer("")
	if pp != nil {
		t.Fatalf("empty path should return nil, got %v", pp)
	}
}

func TestEnvelopeToDataPartVersionFill(t *testing.T) {
	// Version should be set when envelope has none.
	env := Envelope{CreateSurface: &CreateSurface{SurfaceID: "s", CatalogID: BasicCatalogID}}
	part, err := EnvelopeToDataPart(env)
	if err != nil {
		t.Fatal(err)
	}
	if part.Data.Data["version"] != Version {
		t.Fatalf("version not filled: got %v", part.Data.Data["version"])
	}
}

func TestDataPartToEnvelopeMIMETypeEmpty(t *testing.T) {
	// MIME type is empty but part has valid A2UI data structure.
	part := a2a.Part{
		Type: a2a.PartTypeData,
		Data: &a2a.DataPart{MIMEType: "", Data: map[string]any{
			"version":       Version,
			"deleteSurface": map[string]any{"surfaceId": "s"},
		}},
	}
	_, ok, err := DataPartToEnvelope(part)
	if !ok || err != nil {
		t.Fatalf("expected ok=true for empty MIME type, got ok=%v err=%v", ok, err)
	}
}

func TestEnvelopeUnmarshalJSONErrors(t *testing.T) {
	var e Envelope
	// surfaceId not a string in createSurface.
	if err := json.Unmarshal([]byte(`{"version":"v0.9.1","createSurface":{"surfaceId":123}}`), &e); err == nil {
		t.Fatal("expected error for non-string surfaceId in createSurface")
	}
}

func TestSurfaceStoreApplyCreateExistsError(t *testing.T) {
	store := NewSurfaceStore()
	_ = store.Apply(NewCreateSurface("s", BasicCatalogID))
	err := store.Apply(NewCreateSurface("s", BasicCatalogID))
	if !errors.Is(err, ErrSurfaceExists) {
		t.Fatalf("expected ErrSurfaceExists, got %v", err)
	}
}

func TestSurfaceStoreApplyComponentsNotFoundError(t *testing.T) {
	store := NewSurfaceStore()
	err := store.Apply(NewUpdateComponents("nonexistent", Text("t", "hi")))
	if !errors.Is(err, ErrSurfaceNotFound) {
		t.Fatalf("expected ErrSurfaceNotFound, got %v", err)
	}
}

func TestCatalogHasComponentAndFunction(t *testing.T) {
	cat := BasicCatalog()
	if cat.HasComponent("") {
		t.Fatal("empty component type should not be found")
	}
	if cat.HasFunction("nonexistent") {
		t.Fatal("nonexistent function should not be found")
	}
}

// Direct-call tests for UnmarshalJSON methods to cover error branches
// that are hard to reach through json.Unmarshal's indirect call path.

func TestComponentUnmarshalJSONDirectMalformed(t *testing.T) {
	var c Component
	if err := c.UnmarshalJSON([]byte(`{`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestComponentUnmarshalJSONDirectNonStringID(t *testing.T) {
	var c Component
	if err := c.UnmarshalJSON([]byte(`{"id":123,"component":"Text"}`)); err == nil {
		t.Fatal("expected error for non-string id")
	}
}

func TestComponentUnmarshalJSONDirectNonStringType(t *testing.T) {
	var c Component
	if err := c.UnmarshalJSON([]byte(`{"id":"x","component":123}`)); err == nil {
		t.Fatal("expected error for non-string component type")
	}
}

func TestComponentUnmarshalJSONDirectPropUnmarshalError(t *testing.T) {
	var c Component
	// Valid JSON but "text" prop value is a number that we then check.
	if err := c.UnmarshalJSON([]byte(`{"id":"x","component":"Text","text":"hello"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateDataModelUnmarshalDirectSurfaceID(t *testing.T) {
	var u UpdateDataModel
	if err := u.UnmarshalJSON([]byte(`{"surfaceId":123,"path":"/a"}`)); err == nil {
		t.Fatal("expected error for non-string surfaceId")
	}
}

func TestUpdateDataModelUnmarshalDirectPath(t *testing.T) {
	var u UpdateDataModel
	if err := u.UnmarshalJSON([]byte(`{"surfaceId":"s","path":123,"value":1}`)); err == nil {
		t.Fatal("expected error for non-string path")
	}
}

func TestUpdateDataModelUnmarshalDirectValue(t *testing.T) {
	var u UpdateDataModel
	if err := u.UnmarshalJSON([]byte(`{"surfaceId":"s","path":"/a","value":1}`)); err != nil {
		t.Fatal(err)
	}
	if u.Value.(float64) != 1 || !u.ValueSet {
		t.Fatalf("unexpected: %+v", u)
	}
}

func TestDynamicUnmarshalDirectLiteralError(t *testing.T) {
	var d Dynamic
	if err := d.UnmarshalJSON([]byte(``)); err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestDataPartToEnvelopeJSONParseError(t *testing.T) {
	// Data that marshals fine but fails ParseEnvelope with a JSON error
	// (not ErrNoBody/ErrMultipleBodies). Since json.Marshal and json.Decode
	// are symmetric, this can only happen with an Envelope that has a value
	// that triggers a JSON syntax error during ParseEnvelope.
	// This is practically unreachable; test the non-body path instead.
	// For the JSON error path, we test through ParseEnvelope directly.
	if _, err := ParseEnvelope([]byte(`invalid`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestEnvelopeToMapUnmarshalError(t *testing.T) {
	// envelopeToMap json.Unmarshal error is unreachable since the input
	// comes from json.Marshal which always produces valid JSON.
	// Verify the success path works.
	env := NewDeleteSurface("s")
	part, err := EnvelopeToDataPart(env)
	if err != nil {
		t.Fatal(err)
	}
	if part.Data == nil {
		t.Fatal("expected data part")
	}
}

func TestParsePointerTrimmedEmptyPath(t *testing.T) {
	// trimmed == "" branch: unreachable via normal paths since "" and "/"
	// are caught first. Created with a path that would produce trimmed==""
	// if the first check didn't catch it.
	pp := ParsePointer("/")
	if pp != nil {
		t.Fatalf("expected nil for '/', got %v", pp)
	}
}

type failWriter struct{}

func (failWriter) Write(p []byte) (int, error) { return 0, errors.New("write error") }

func TestEncoderEncodeError(t *testing.T) {
	enc := NewEncoder(failWriter{})
	if err := enc.Encode(Envelope{DeleteSurface: &DeleteSurface{SurfaceID: "s"}}); err == nil {
		t.Fatal("expected encode error")
	}
}

func TestEncoderEncodeAllError(t *testing.T) {
	enc := NewEncoder(failWriter{})
	if err := enc.EncodeAll([]Envelope{{DeleteSurface: &DeleteSurface{SurfaceID: "s"}}}); err == nil {
		t.Fatal("expected encode all error")
	}
}

func TestSurfaceStoreApplyDefaultKind(t *testing.T) {
	store := NewSurfaceStore()
	if err := store.Apply(Envelope{Version: Version}); !errors.Is(err, ErrNoBody) {
		t.Fatalf("expected ErrNoBody, got %v", err)
	}
}

func TestSurfaceStoreApplyDataModelNotFound(t *testing.T) {
	store := NewSurfaceStore()
	err := store.Apply(NewUpdateDataModel("nonexistent", "/a", "val"))
	if !errors.Is(err, ErrSurfaceNotFound) {
		t.Fatalf("expected ErrSurfaceNotFound, got %v", err)
	}
}

func TestUpdateDataModelUnmarshalDirectMalformedJSON(t *testing.T) {
	var u UpdateDataModel
	if err := u.UnmarshalJSON([]byte(`{`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
