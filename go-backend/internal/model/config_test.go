package model

import "testing"

func TestDefaultConfigMatchesJavaEpisodeAndProxyDefaults(t *testing.T) {
	cfg := DefaultConfig()
	wantEpisode := `(.*|\[.*])(( - |Vol |[Ee][Pp]?)\d+(\.5)?( ?\(\d+\))?|【\d+(\.5)?】|\[\d+(\.5)?( ?[vV]\d)?( ?END)?( ?完)?( ?FIN)?]|第\d+(\.5)?[话話集]( - END)?|^\[TOC].* \d+|^六四位元字幕组.*★\d+(\.5)?★)`
	if got := cfg["customEpisodeStr"]; got != wantEpisode {
		t.Fatalf("customEpisodeStr = %q, want %q", got, wantEpisode)
	}
	wantProxyList := "mikanani.me\nmikanime.tv\nanibt.net\nanimes.garden\nnyaa.si\nacg.rip\ngoogle.com\ntmdb.org\nthemoviedb.org\nanilist.co\nwushuo.top\nbgm.tv\nbangumi.tv\nchii.in\ngithub.com\nraw.githubusercontent.com\ntelegram.org\n"
	if got := cfg["proxyList"]; got != wantProxyList {
		t.Fatalf("proxyList = %q, want trailing newline", got)
	}
}

func TestDefaultConfigIncludesJavaExposedRuntimeFields(t *testing.T) {
	cfg := DefaultConfig()
	want := map[string]any{
		"expirationTime":       int64(0),
		"outTradeNo":           "",
		"tryOut":               false,
		"verifyExpirationTime": false,
	}
	for key, expected := range want {
		if got, ok := cfg[key]; !ok || got != expected {
			t.Fatalf("default config %q = %#v (present=%t), want %#v", key, got, ok, expected)
		}
	}
}
