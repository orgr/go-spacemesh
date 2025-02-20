package tests

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	spacemeshv1 "github.com/spacemeshos/api/release/go/spacemesh/v1"
	"github.com/spacemeshos/ed25519"
	"golang.org/x/sync/errgroup"

	"github.com/spacemeshos/go-spacemesh/systest/chaos"
	"github.com/spacemeshos/go-spacemesh/systest/cluster"
	"github.com/spacemeshos/go-spacemesh/systest/testcontext"
)

type transaction struct {
	Nonce     uint64
	Recipient [20]byte
	GasLimit  uint64
	Fee       uint64
	Amount    uint64
}

func encodeTx(tx transaction) (buf []byte) {
	scratch := [8]byte{}
	binary.BigEndian.PutUint64(scratch[:], tx.Nonce)
	buf = append(buf, scratch[:]...)
	buf = append(buf, tx.Recipient[:]...)
	binary.BigEndian.PutUint64(scratch[:], tx.GasLimit)
	buf = append(buf, scratch[:]...)
	binary.BigEndian.PutUint64(scratch[:], tx.Fee)
	buf = append(buf, scratch[:]...)
	binary.BigEndian.PutUint64(scratch[:], tx.Amount)
	buf = append(buf, scratch[:]...)
	return buf
}

func submitTransacition(ctx context.Context, pk ed25519.PrivateKey, tx transaction, node *cluster.NodeClient) error {
	txclient := spacemeshv1.NewTransactionServiceClient(node)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	encoded := encodeTx(tx)
	encoded = append(encoded, ed25519.Sign2(pk, encoded)...)
	response, err := txclient.SubmitTransaction(ctx, &spacemeshv1.SubmitTransactionRequest{Transaction: encoded})
	if err != nil {
		return err
	}
	if response.Txstate == nil {
		return fmt.Errorf("tx state should not be nil")
	}
	return nil
}

func newTransactionSubmitter(pk ed25519.PrivateKey, receiver [20]byte, amount uint64, client *cluster.NodeClient) func(context.Context) error {
	var nonce uint64
	return func(ctx context.Context) error {
		if err := submitTransacition(ctx, pk, transaction{
			GasLimit:  100,
			Fee:       1,
			Amount:    amount,
			Recipient: receiver,
			Nonce:     nonce,
		}, client); err != nil {
			return err
		}
		nonce++
		return nil
	}
}

func extractNames(nodes ...*cluster.NodeClient) []string {
	var rst []string
	for _, n := range nodes {
		rst = append(rst, n.Name)
	}
	return rst
}

func watchLayers(ctx context.Context, eg *errgroup.Group, client *cluster.NodeClient,
	collector func(*spacemeshv1.LayerStreamResponse) (bool, error)) {
	eg.Go(func() error {
		meshapi := spacemeshv1.NewMeshServiceClient(client)
		layers, err := meshapi.LayerStream(ctx, &spacemeshv1.LayerStreamRequest{})
		if err != nil {
			return err
		}
		for {
			layer, err := layers.Recv()
			if err != nil {
				return err
			}
			if cont, err := collector(layer); !cont {
				return err
			}
		}
	})
}

func watchProposals(ctx context.Context, eg *errgroup.Group, client *cluster.NodeClient, collector func(*spacemeshv1.Proposal) (bool, error)) {
	eg.Go(func() error {
		dbg := spacemeshv1.NewDebugServiceClient(client)
		proposals, err := dbg.ProposalsStream(ctx, &empty.Empty{})
		if err != nil {
			return fmt.Errorf("proposal stream for %s: %w", client.Name, err)
		}
		for {
			proposal, err := proposals.Recv()
			if err != nil {
				return fmt.Errorf("proposal event for %s: %w", client.Name, err)
			}
			if cont, err := collector(proposal); !cont {
				return err
			}
		}
	})
}

func prettyHex(buf []byte) string {
	return fmt.Sprintf("0x%x", buf)
}

func scheduleChaos(ctx context.Context, eg *errgroup.Group, client *cluster.NodeClient, from, to uint32, action func(context.Context) (chaos.Teardown, error)) {
	var teardown chaos.Teardown
	watchLayers(ctx, eg, client, func(layer *spacemeshv1.LayerStreamResponse) (bool, error) {
		if layer.Layer.Number.Number == from && teardown == nil {
			var err error
			teardown, err = action(ctx)
			if err != nil {
				return false, err
			}
		}
		if layer.Layer.Number.Number == to {
			if err := teardown(ctx); err != nil {
				return false, err
			}
			return false, nil
		}
		return true, nil
	})
}

func currentLayer(ctx context.Context, client *cluster.NodeClient) (uint32, error) {
	response, err := spacemeshv1.NewMeshServiceClient(client).CurrentLayer(ctx, &spacemeshv1.CurrentLayerRequest{})
	if err != nil {
		return 0, err
	}
	return response.Layernum.Number, nil
}

func waitAll(tctx *testcontext.Context, cl *cluster.Cluster) error {
	var eg errgroup.Group
	for i := 0; i < cl.Total(); i++ {
		i := i
		eg.Go(func() error {
			return cl.Wait(tctx, i)
		})
	}
	return eg.Wait()
}

func min(i, j int) int {
	if i < j {
		return i
	}
	return j
}
