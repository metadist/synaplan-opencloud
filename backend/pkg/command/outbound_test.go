package command

import "testing"

func TestOutboundTransport(t *testing.T) {
	verified := outboundTransport(false)
	if verified.TLSClientConfig != nil && verified.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("verification stays on unless OC_INSECURE is set")
	}
	skipped := outboundTransport(true)
	if skipped.TLSClientConfig == nil || !skipped.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("OC_INSECURE skips certificate verification")
	}
}
