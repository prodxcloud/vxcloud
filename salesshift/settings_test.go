package salesshift

import (
	"encoding/json"
	"testing"
)

func TestDecodeDataUnwrapsEnvelope(t *testing.T) {
	var p FooterPreview
	if err := decodeData(json.RawMessage(`{"success":true,"data":{"address":"1 Main St","address_source":"mailbox"}}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Address != "1 Main St" || p.AddressSource != "mailbox" {
		t.Fatalf("envelope not unwrapped: %+v", p)
	}
	var flat FooterPreview
	if err := decodeData(json.RawMessage(`{"success":true,"html":"<p>x</p>","footer_text_source":"default"}`), &flat); err != nil {
		t.Fatal(err)
	}
	if flat.HTML != "<p>x</p>" || flat.FooterTextSource != "default" {
		t.Fatalf("flat body not decoded: %+v", flat)
	}
	var rows []TemplateUsage
	if err := decodeData(json.RawMessage(`[{"step_id":"s1","out_of_date":true}]`), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].OutOfDate {
		t.Fatalf("bare array not decoded: %+v", rows)
	}
}

func TestCountOrList(t *testing.T) {
	var r ListMembersResult
	if err := json.Unmarshal([]byte(`{"added":2,"already_in_list":3,"not_found":["a@x.com"]}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Added != 2 || r.AlreadyInList.Count != 3 || r.NotFound.Count != 1 || len(r.NotFound.Items) != 1 {
		t.Fatalf("unexpected: %+v", r)
	}
	if err := json.Unmarshal([]byte(`{"not_found":null}`), &r); err != nil || r.NotFound.Count != 0 {
		t.Fatalf("null not handled: %v %+v", err, r)
	}
}

func TestFooterPatchSemantics(t *testing.T) {
	empty := ""
	body := map[string]any{}
	FooterPatch{FooterAddress: &empty, InheritFooterText: true}.into(body)
	if v, ok := body["footer_address"]; !ok || v != "" {
		t.Fatalf(`"" must be sent as "no address": %#v`, body)
	}
	if v, ok := body["footer_text"]; !ok || v != nil {
		t.Fatalf("inherit must be an explicit null: %#v", body)
	}
	raw, _ := json.Marshal(body)
	if string(raw) != `{"footer_address":"","footer_text":null}` {
		t.Fatalf("wire form: %s", raw)
	}
	none := map[string]any{}
	FooterPatch{}.into(none)
	if len(none) != 0 {
		t.Fatalf("unset fields must be omitted: %#v", none)
	}
}

func TestContactSelectorExactlyOne(t *testing.T) {
	if _, err := (ContactSelector{}).body("X"); err == nil {
		t.Fatal("empty selector accepted")
	}
	if _, err := (ContactSelector{ContactIDs: []string{"a"}, Emails: []string{"b@x.com"}}).body("X"); err == nil {
		t.Fatal("two selectors accepted")
	}
	b, err := (ContactSelector{Filter: ContactFilter{Search: "dental", Page: 3}.Params()}).body("X")
	if err != nil {
		t.Fatal(err)
	}
	f := b["filter"].(map[string]any)
	if f["search"] != "dental" {
		t.Fatalf("filter lost search: %#v", f)
	}
	if _, ok := f["page"]; ok {
		t.Fatalf("paging must not reach a filter: %#v", f)
	}
}
