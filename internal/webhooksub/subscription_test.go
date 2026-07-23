package webhooksub

import "testing"

func TestDecodeAcceptsBothEntryForms(t *testing.T) {
	subs, err := DecodeStrict(`["http://a.test/hook",{"url":"http://b.test/hook","events":["container","task"]},{"url":"http://c.test/hook"}]`)
	if err != nil {
		t.Fatalf("DecodeStrict: %v", err)
	}
	want := []Subscription{
		{URL: "http://a.test/hook"},
		{URL: "http://b.test/hook", Events: []string{"container", "task"}},
		{URL: "http://c.test/hook"},
	}
	if !Equal(subs, want) {
		t.Fatalf("decoded = %+v, want %+v", subs, want)
	}
}

// A list written as plain strings must come back out as plain strings: the
// stored column is read by the dispatcher and by anything that predates the
// structured form.
func TestEncodePreservesBareForm(t *testing.T) {
	raw := `["http://a.test/hook",{"url":"http://b.test/hook","events":["container"]}]`
	subs, err := DecodeStrict(raw)
	if err != nil {
		t.Fatalf("DecodeStrict: %v", err)
	}
	encoded, err := Encode(subs)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if encoded != raw {
		t.Fatalf("round-trip = %s, want %s", encoded, raw)
	}
}

func TestEncodeNilIsEmptyArray(t *testing.T) {
	encoded, err := Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if encoded != "[]" {
		t.Fatalf("Encode(nil) = %s, want []", encoded)
	}
}

func TestDecodeToleratesMalformedColumn(t *testing.T) {
	bad := "not json"
	if got := Decode(&bad); got != nil {
		t.Fatalf("Decode(malformed) = %+v, want nil", got)
	}
	if got := Decode(nil); got != nil {
		t.Fatalf("Decode(nil) = %+v, want nil", got)
	}
	if _, err := DecodeStrict(`[42]`); err == nil {
		t.Fatal("DecodeStrict must reject an entry that is neither string nor object")
	}
}
