/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package discovery

import (
	"testing"

	"github.com/hyperledger/fabric-sdk-go/pkg/client/common/discovery/staticdiscovery"
	"github.com/hyperledger/fabric-sdk-go/pkg/common/providers/fab"
	"github.com/hyperledger/fabric-sdk-go/pkg/core/config"
	"github.com/hyperledger/fabric-sdk-go/pkg/fab/peer"
)

type mockFilter struct {
	called bool
}

// Accept returns true if this peer is to be included in the target list
func (df *mockFilter) Accept(peer fab.Peer) bool {
	df.called = true
	return true
}

func TestDiscoveryFilter(t *testing.T) {

	configBackend, err := config.FromFile("../../../../test/fixtures/config/config_test.yaml")()
	if err != nil {
		t.Fatalf(err.Error())
	}

	_, config1, _, err := config.FromBackend(configBackend)()
	if err != nil {
		t.Fatalf(err.Error())
	}

	peerCreator := defPeerCreator{defPeerConfig: config1}
	discoveryProvider, err := staticdiscovery.New(config1, &peerCreator)
	if err != nil {
		t.Fatalf("Failed to  setup discovery provider: %s", err)
	}

	discoveryService, err := discoveryProvider.CreateDiscoveryService("mychannel")
	if err != nil {
		t.Fatalf("Failed to setup discovery service: %s", err)
	}

	discoveryFilter := &mockFilter{called: false}

	discoveryService = NewDiscoveryFilterService(discoveryService, discoveryFilter)

	peers, err := discoveryService.GetPeers()
	if err != nil {
		t.Fatalf("Failed to get peers from discovery service: %s", err)
	}

	// One peer is configured for "mychannel"
	expectedNumOfPeers := 1
	if len(peers) != expectedNumOfPeers {
		t.Fatalf("Expecting %d, got %d peers", expectedNumOfPeers, len(peers))
	}

	if !discoveryFilter.called {
		t.Fatalf("Expecting true, got false")
	}

}

type defPeerCreator struct {
	defPeerConfig fab.EndpointConfig
}

func (pc *defPeerCreator) CreatePeerFromConfig(peerCfg *fab.NetworkPeer) (fab.Peer, error) {
	return peer.New(pc.defPeerConfig, peer.FromPeerConfig(peerCfg))
}
