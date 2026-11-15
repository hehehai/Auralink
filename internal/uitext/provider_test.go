package uitext

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"testing"

	"github.com/hehehai/Auralink/internal/asr"
	"github.com/hehehai/Auralink/internal/llm"
	"github.com/hehehai/Auralink/internal/netx"
)

func TestProviderErrorNetworkHints(t *testing.T) {
	noRoute := &llm.Error{Code: llm.CodeNetwork, Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.EHOSTUNREACH}}
	public := &asr.Error{Kind: asr.KindNetwork, Provider: "openai", Err: fmt.Errorf("Post: %w", &netx.PlainPublicError{Host: "gpubox", Addr: netip.MustParseAddr("203.0.113.7")})}
	refused := &asr.Error{Kind: asr.KindNetwork, Provider: "openai", Err: fmt.Errorf("connect: connection refused")}

	if msg, _ := ProviderErrorText(noRoute); !strings.Contains(msg, "Local Network") {
		t.Errorf("no route: %q", msg)
	}
	if s := ProviderErrorShort(noRoute); !strings.Contains(s, "Local Network") {
		t.Errorf("no route, short: %q", s)
	}
	if msg, _ := ProviderErrorText(public); !strings.Contains(msg, "public") {
		t.Errorf("public: %q", msg)
	}
	if s := ProviderErrorShort(public); !strings.Contains(s, "https://") {
		t.Errorf("public, short: %q", s)
	}
	if s := ProviderErrorShort(refused); strings.Contains(s, "Local Network") || strings.Contains(s, "Public") {
		t.Errorf("refused, short: %q", s)
	}
}
