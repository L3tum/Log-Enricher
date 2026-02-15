package network

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCustomResolver_UsesSystemResolverWhenServerEmpty(t *testing.T) {
	assert.Same(t, net.DefaultResolver, CustomResolver(""))
	assert.Same(t, net.DefaultResolver, CustomResolver("   "))
}

func TestCustomResolver_UsesCustomResolverWhenServerSet(t *testing.T) {
	assert.NotSame(t, net.DefaultResolver, CustomResolver("1.1.1.1:53"))
}
