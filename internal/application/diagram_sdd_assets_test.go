package application

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram/sddfile"
)

// An embedded document keeps {mime} only; the export fills the bytes from the
// asset store by hash, and drops an entry whose bytes are gone.
func TestWithAssetBytes(t *testing.T) {
	e := newSharing(t)
	d := e.Diagrams
	a := must1[StoredAsset](t)(d.StoreAsset(dctx, alice, pngBytes))
	gone := diagram.HashBytes([]byte("never stored"))
	inline := base64.StdEncoding.EncodeToString([]byte("x"))
	doc := sddfile.Doc{Data: json.RawMessage(`{"version":2,"assets":{"` + a.Hash + `":{"mime":"image/png"},"` + gone + `":{"mime":"image/png"},"` + diagram.HashBytes([]byte("x")) + `":{"mime":"image/png","data":"` + inline + `"}}}`)}

	var out struct {
		Assets map[string]struct{ Mime, Data string } `json:"assets"`
	}
	if err := json.Unmarshal(d.withAssetBytes(dctx, doc).Data, &out); err != nil {
		t.Fatal(err)
	}
	if got, _ := base64.StdEncoding.DecodeString(out.Assets[a.Hash].Data); string(got) != string(pngBytes) {
		t.Errorf("stored asset not filled: %q", out.Assets[a.Hash].Data)
	}
	if _, ok := out.Assets[gone]; ok {
		t.Error("missing asset kept")
	}
	if len(out.Assets) != 2 || out.Assets[diagram.HashBytes([]byte("x"))].Data != inline {
		t.Errorf("inline asset changed: %+v", out.Assets)
	}
}
