/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership

import (
	"fmt"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-sdk-go/pkg/common/providers/fab"
	"github.com/hyperledger/fabric-sdk-go/pkg/fab/mocks"
	"github.com/hyperledger/fabric-sdk-go/pkg/util/concurrent/lazyref"
	mb "github.com/hyperledger/fabric-sdk-go/third_party/github.com/hyperledger/fabric/protos/msp"
	"github.com/stretchr/testify/assert"
)

type badKey struct {
	s string
}

func (b *badKey) String() string {
	return b.s
}

func TestMembershipCache(t *testing.T) {
	testChannelID := "test"
	goodMSPID := "GoodMSP"

	cfg := mocks.NewMockChannelCfg(testChannelID)
	cfg.MockMSPs = []*mb.MSPConfig{buildMSPConfig(goodMSPID, []byte(validRootCA))}

	ctx := mocks.NewMockProviderContext()

	cache := NewRefCache(time.Millisecond * 10)
	assert.NotNil(t, cache)

	key, err := NewCacheKey(Context{Providers: ctx, EndpointConfig: mocks.NewMockEndpointConfig()}, lazyref.New(func() (interface{}, error) { return cfg, nil }), testChannelID)
	assert.Nil(t, err)
	assert.NotNil(t, key)

	ch := key.ChannelID()
	assert.Equal(t, testChannelID, ch)

	r, err := cache.Get(key)
	assert.Nil(t, err)
	assert.NotNil(t, r)

	mem, ok := r.(fab.ChannelMembership)
	assert.True(t, ok)

	sID := &mb.SerializedIdentity{Mspid: goodMSPID, IdBytes: []byte(certPem)}
	goodEndorser, err := proto.Marshal(sID)
	assert.Nil(t, err)

	err = mem.Validate(goodEndorser)
	assert.Nil(t, err)

	err = mem.Verify(goodEndorser, []byte("test"), []byte("test1"))
	assert.Nil(t, err)
}

func TestMembershipCacheBad(t *testing.T) {
	testChannelID := "test"
	testErr := fmt.Errorf("bad initializer")

	ctx := mocks.NewMockProviderContext()

	cache := NewRefCache(time.Millisecond * 10)
	assert.NotNil(t, cache)

	r, err := cache.Get(&badKey{s: "test"})
	assert.NotNil(t, err)
	assert.Equal(t, "unexpected cache key", err.Error())
	assert.Nil(t, r)

	key, err := NewCacheKey(Context{Providers: ctx, EndpointConfig: mocks.NewMockEndpointConfig()}, lazyref.New(func() (interface{}, error) { return nil, testErr }), testChannelID)
	assert.Nil(t, err)
	assert.NotNil(t, key)

	r, err = cache.Get(key)
	assert.Nil(t, err)
	assert.NotNil(t, r)

	mem, ok := r.(fab.ChannelMembership)
	assert.True(t, ok)

	err = mem.Validate([]byte("MSP"))
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), testErr.Error())

	err = mem.Verify([]byte("MSP"), []byte("test"), []byte("test1"))
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), testErr.Error())
}
