package i18n

import "testing"

func TestMatching(t *testing.T) {
	for _, v := range []struct{ in, want string }{{"en-US", "en"}, {"zh-Hans-CN", "zh-Hans"}, {"zh_CN", "zh-Hans"}, {"zh-SG", "zh-Hans"}, {"zh-Hant", "en"}, {"ja", "en"}} {
		if got := New(v.in).Language; got != v.want {
			t.Errorf("%s: %s", v.in, got)
		}
	}
}
func TestCatalogParity(t *testing.T) {
	for k := range en {
		if zh[k] == "" {
			t.Errorf("missing Chinese %s", k)
		}
	}
	for k := range zh {
		if en[k] == "" {
			t.Errorf("missing English %s", k)
		}
	}
}
