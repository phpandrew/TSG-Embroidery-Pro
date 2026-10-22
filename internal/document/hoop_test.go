package document

import "testing"

func TestDefaultHoopIs4x4(t *testing.T) {
	h := HoopByID(DefaultSettings().Hoop)
	if h.ID != "in4x4" || h.W != 1016 || h.H != 1016 {
		t.Fatalf("default hoop = %+v", h)
	}
	if HoopByID("nope").ID != "in4x4" {
		t.Fatal("unknown hoop should fall back to 4x4")
	}
	if c := HoopByID("hat4x2"); c.W != 1016 || c.H != 508 {
		t.Fatalf("hat hoop = %+v", c)
	}
}

func TestDefaultsCarryToNewDocs(t *testing.T) {
	e := NewEditor(nil)
	s := DefaultSettings()
	s.Hoop = "hat4x2"
	s.SPM = 650
	e.SetDefaults(s)
	if e.Settings().Hoop != "hat4x2" {
		t.Fatal("empty startup doc should pick up saved defaults")
	}
	e.Reset()
	if got := e.Settings(); got.Hoop != "hat4x2" || got.SPM != 650 {
		t.Fatalf("new doc settings = %+v", got)
	}
}
