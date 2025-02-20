package state

import (
	crand "crypto/rand"
	"math/rand"
	"testing"

	"github.com/spacemeshos/ed25519"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/database"
	"github.com/spacemeshos/go-spacemesh/log/logtest"
	"github.com/spacemeshos/go-spacemesh/signing"
	"github.com/spacemeshos/go-spacemesh/svm/transaction"
)

type ProcessorStateSuite struct {
	suite.Suite
	db        *database.LDBDatabase
	processor *TransactionProcessor
}

type appliedTxsMock struct{}

func (appliedTxsMock) Put(key []byte, value []byte) error { return nil }
func (appliedTxsMock) Delete(key []byte) error            { panic("implement me") }
func (appliedTxsMock) Get(key []byte) ([]byte, error)     { panic("implement me") }
func (appliedTxsMock) Has(key []byte) (bool, error)       { panic("implement me") }
func (appliedTxsMock) Close()                             { panic("implement me") }
func (appliedTxsMock) NewBatch() database.Batch           { panic("implement me") }
func (appliedTxsMock) Find(key []byte) database.Iterator  { panic("implement me") }

func (s *ProcessorStateSuite) SetupTest() {
	lg := logtest.New(s.T()).WithName("proc_logger")
	s.db = database.NewMemDatabase()
	s.processor = NewTransactionProcessor(s.db, appliedTxsMock{}, lg)
}

func createAccount(state *TransactionProcessor, addr types.Address, balance int64, nonce uint64) *Object {
	obj1 := state.GetOrNewStateObj(addr)
	obj1.AddBalance(uint64(balance))
	obj1.SetNonce(nonce)
	state.updateStateObj(obj1)
	return obj1
}

func createSpawnTransaction(destination types.Address, signer *signing.EdSigner) *types.Transaction {
	tx := transaction.GenerateSpawnTransaction(signer, destination)
	return tx
}

func createCallTransaction(t *testing.T, nonce uint64, destination types.Address, amount, fee uint64, signer *signing.EdSigner) *types.Transaction {
	tx, err := transaction.GenerateCallTransaction(signer, destination, nonce, amount, 100, fee)
	assert.NoError(t, err)
	return tx
}

func (s *ProcessorStateSuite) TestTransactionProcessor_ApplyTransaction() {
	// test happy flow
	// test happy flow with underlying structures
	// test insufficient funds
	// test wrong nonce
	// test account doesn't exist
	signerBuf := []byte("22222222222222222222222222222222")
	signerBuf = append(signerBuf, []byte{
		94, 33, 44, 9, 128, 228, 179, 159, 192, 151, 33, 19, 74, 160, 33, 9,
		55, 78, 223, 210, 96, 192, 211, 208, 60, 181, 1, 200, 214, 84, 87, 169,
	}...)
	signer, err := signing.NewEdSignerFromBuffer(signerBuf)
	assert.NoError(s.T(), err)
	obj1 := createAccount(s.processor, SignerToAddr(signer), 21, 0)
	obj2 := createAccount(s.processor, toAddr([]byte{0x01, 0o2}), 1, 10)
	createAccount(s.processor, toAddr([]byte{0x02}), 44, 0)
	s.processor.Commit()

	transactions := []*types.Transaction{
		createSpawnTransaction(obj2.address, signer),
		createCallTransaction(s.T(), obj1.Nonce()+1, obj2.address, 1, 5, signer),
	}

	failed, err := s.processor.ApplyTransactions(types.NewLayerID(1), transactions)
	assert.NoError(s.T(), err)
	assert.Empty(s.T(), failed)

	got := string(s.processor.Dump())

	assert.Equal(s.T(), uint64(15), s.processor.GetBalance(obj1.address))
	assert.Equal(s.T(), uint64(2), s.processor.GetNonce(obj1.address))

	want := `{
	"root": "3f1b129ed8172becbff49f6365ac719d84698b55eaab48be7874aee9c8e5db68",
	"accounts": {
		"0000000000000000000000000000000000000002": {
			"nonce": 0,
			"balance": 44
		},
		"0000000000000000000000000000000000000102": {
			"nonce": 10,
			"balance": 2
		},
		"4aa02109374edfd260c0d3d03cb501c8d65457a9": {
			"nonce": 2,
			"balance": 15
		}
	}
}`
	require.Equal(s.T(), want, got)
}

func SignerToAddr(signer *signing.EdSigner) types.Address {
	return types.GenerateAddress(signer.PublicKey().Bytes())
}

/*func (s *ProcessorStateSuite) TestTransactionProcessor_ApplyTransaction_DoubleTrans() {
	//test happy flow
	//test happy flow with underlying structures
	//test insufficient funds
	//test wrong nonce
	//test account doesn't exist
	obj1 := createAccount(s.processor, []byte{0x01}, 21, 0)
	obj2 := createAccount(s.processor, []byte{0x01, 02}, 1, 10)
	createAccount(s.processor, []byte{0x02}, 44, 0)
	s.processor.Commit(false)

	transactions := Transactions{
		createCallTransaction(obj1.Nonce(), obj1.address, obj2.address, 1),
		createCallTransaction(obj1.Nonce(), obj1.address, obj2.address, 1),
		createCallTransaction(obj1.Nonce(), obj1.address, obj2.address, 1),
		createCallTransaction(obj1.Nonce(), obj1.address, obj2.address, 1),
	}

	failed, err := s.processor.ApplyTransactions(1, transactions)
	assert.NoError(s.T(), err)
	assert.True(s.T(), failed == 0)

	got := string(s.processor.Dump())

	assert.Equal(s.T(), big.NewInt(20), s.processor.GetBalance(obj1.address))

	want := `{
	"root": "7ed462059ad2df6754b5aa1f3d8a150bb9b0e1c4eb50b6217a8fc4ecbec7fb28",
	"accounts": {
		"0000000000000000000000000000000000000001": {
			"balance": "20",
			"nonce": 1
		},
		"0000000000000000000000000000000000000002": {
			"balance": "44",
			"nonce": 0
		},
		"0000000000000000000000000000000000000102": {
			"balance": "2",
			"nonce": 10
		}
	}
}`
	require.Equal(s.T(), want, got)
}*/

func (s *ProcessorStateSuite) TestTransactionProcessor_ApplyTransaction_Errors() {
	signer1 := signing.NewEdSigner()
	obj1 := createAccount(s.processor, SignerToAddr(signer1), 21, 0)
	obj2 := createAccount(s.processor, toAddr([]byte{0x01, 0o2}), 1, 10)
	createAccount(s.processor, toAddr([]byte{0x02}), 44, 0)
	s.processor.Commit()

	transactions := []*types.Transaction{
		createSpawnTransaction(obj2.address, signer1),
		createCallTransaction(s.T(), obj1.Nonce()+1, obj2.address, 1, 5, signer1),
	}

	failed, err := s.processor.ApplyTransactions(types.NewLayerID(1), transactions)
	assert.NoError(s.T(), err)
	assert.Empty(s.T(), failed)

	err = s.processor.ApplyTransaction(createCallTransaction(s.T(), 0, obj2.address, 1, 5, signer1), types.LayerID{})
	assert.Error(s.T(), err)
	assert.Equal(s.T(), err.Error(), errNonce)

	err = s.processor.ApplyTransaction(createCallTransaction(s.T(), obj1.Nonce(), obj2.address, 21, 5, signer1), types.LayerID{})
	assert.Error(s.T(), err)
	assert.Equal(s.T(), err.Error(), errFunds)

	// Test origin
	err = s.processor.ApplyTransaction(createCallTransaction(s.T(), obj1.Nonce(), obj2.address, 21, 5, signing.NewEdSigner()), types.LayerID{})
	assert.Error(s.T(), err)
	assert.Equal(s.T(), err.Error(), errOrigin)
}

func (s *ProcessorStateSuite) TestTransactionProcessor_ApplyRewards() {
	s.processor.ApplyRewards(types.NewLayerID(1), map[types.Address]uint64{
		types.HexToAddress("aaa"): 2000,
		types.HexToAddress("bbb"): 2000,
		types.HexToAddress("ccc"): 1000,
		types.HexToAddress("ddd"): 1000,
	},
	)

	assert.Equal(s.T(), s.processor.GetBalance(types.HexToAddress("aaa")), uint64(2000))
	assert.Equal(s.T(), s.processor.GetBalance(types.HexToAddress("bbb")), uint64(2000))
	assert.Equal(s.T(), s.processor.GetBalance(types.HexToAddress("ccc")), uint64(1000))
	assert.Equal(s.T(), s.processor.GetBalance(types.HexToAddress("ddd")), uint64(1000))
}

func (s *ProcessorStateSuite) TestTransactionProcessor_ApplyTransaction_OrderByNonce() {
	signerBuf := []byte("22222222222222222222222222222222")
	signerBuf = append(signerBuf, []byte{
		94, 33, 44, 9, 128, 228, 179, 159, 192, 151, 33, 19, 74, 160, 33, 9,
		55, 78, 223, 210, 96, 192, 211, 208, 60, 181, 1, 200, 214, 84, 87, 169,
	}...)
	signer, err := signing.NewEdSignerFromBuffer(signerBuf)
	assert.NoError(s.T(), err)
	obj1 := createAccount(s.processor, SignerToAddr(signer), 25, 0)
	obj2 := createAccount(s.processor, toAddr([]byte{0x01, 0o2}), 1, 10)
	obj3 := createAccount(s.processor, toAddr([]byte{0x02}), 44, 0)
	_, err = s.processor.Commit()
	assert.NoError(s.T(), err)

	transactions := []*types.Transaction{
		createCallTransaction(s.T(), obj1.Nonce()+4, obj3.address, 1, 5, signer),
		createCallTransaction(s.T(), obj1.Nonce()+3, obj3.address, 1, 5, signer),
		createCallTransaction(s.T(), obj1.Nonce()+2, obj3.address, 1, 5, signer),
		createCallTransaction(s.T(), obj1.Nonce()+1, obj2.address, 1, 5, signer),
		createSpawnTransaction(obj2.address, signer),
	}

	s.processor.ApplyTransactions(types.NewLayerID(1), transactions)
	// assert.Error(s.T(), err)

	got := string(s.processor.Dump())

	assert.Equal(s.T(), uint64(1), s.processor.GetBalance(obj1.address))
	assert.Equal(s.T(), uint64(2), s.processor.GetBalance(obj2.address))

	want := `{
	"root": "3e29fe62d68ca95ef051050505ea33192e0dd7f057c1fa675540bfdc868b3f46",
	"accounts": {
		"0000000000000000000000000000000000000002": {
			"nonce": 0,
			"balance": 47
		},
		"0000000000000000000000000000000000000102": {
			"nonce": 10,
			"balance": 2
		},
		"4aa02109374edfd260c0d3d03cb501c8d65457a9": {
			"nonce": 5,
			"balance": 1
		}
	}
}`
	require.Equal(s.T(), want, got)
}

func (s *ProcessorStateSuite) TestTransactionProcessor_Reset() {
	lg := logtest.New(s.T()).WithName("proc_logger")
	txDb := database.NewMemDatabase()
	db := database.NewMemDatabase()
	processor := NewTransactionProcessor(db, txDb, lg)

	signer1Buf := []byte("22222222222222222222222222222222")
	signer1Buf = append(signer1Buf, []byte{
		94, 33, 44, 9, 128, 228, 179, 159, 192, 151, 33, 19, 74, 160, 33, 9,
		55, 78, 223, 210, 96, 192, 211, 208, 60, 181, 1, 200, 214, 84, 87, 169,
	}...)
	signer2Buf := []byte("33333333333333333333333333333333")
	signer2Buf = append(signer2Buf, []byte{
		23, 203, 121, 251, 43, 65, 32, 242, 177, 236, 101, 228, 25, 141, 110, 8,
		178, 142, 129, 63, 235, 1, 228, 164, 0, 131, 155, 133, 225, 128, 128, 206,
	}...)

	signer1, err := signing.NewEdSignerFromBuffer(signer1Buf)
	assert.NoError(s.T(), err)
	signer2, err := signing.NewEdSignerFromBuffer(signer2Buf)
	assert.NoError(s.T(), err)
	obj1 := createAccount(processor, SignerToAddr(signer1), 21, 0)
	obj2 := createAccount(processor, SignerToAddr(signer2), 41, 10)
	createAccount(processor, toAddr([]byte{0x02}), 44, 0)
	processor.Commit()

	transactions := []*types.Transaction{
		createSpawnTransaction(obj2.address, signer1),
		createCallTransaction(s.T(), obj1.Nonce()+1, obj2.address, 1, 5, signer1),
		// createCallTransaction(obj2.Nonce(),obj2.address, obj1.address, 1),
	}

	failed, err := processor.ApplyTransactions(types.NewLayerID(1), transactions)
	assert.NoError(s.T(), err)
	assert.Empty(s.T(), failed)

	transactions = []*types.Transaction{
		createCallTransaction(s.T(), obj1.Nonce(), obj2.address, 1, 5, signer1),
		createCallTransaction(s.T(), obj2.Nonce(), obj1.address, 10, 5, signer2),
	}

	failed, err = processor.ApplyTransactions(types.NewLayerID(2), transactions)
	assert.Empty(s.T(), failed)
	assert.NoError(s.T(), err)

	got := string(processor.Dump())

	want := `{
	"root": "65d4e37884079f7df312b3932626f0dd20cc1ae562ed239611f7e0eeee0395d5",
	"accounts": {
		"0000000000000000000000000000000000000002": {
			"nonce": 0,
			"balance": 44
		},
		"198d6e08b28e813feb01e4a400839b85e18080ce": {
			"nonce": 11,
			"balance": 28
		},
		"4aa02109374edfd260c0d3d03cb501c8d65457a9": {
			"nonce": 3,
			"balance": 19
		}
	}
}`
	require.Equal(s.T(), want, got)

	err = processor.LoadState(types.NewLayerID(1))
	assert.NoError(s.T(), err)

	got = string(processor.Dump())

	assert.Equal(s.T(), uint64(15), processor.GetBalance(obj1.address))

	want = `{
	"root": "4896f8b92858e67cad9c2276dad453ba7d92d5c1bf21f701bba3ce2a2c52010b",
	"accounts": {
		"0000000000000000000000000000000000000002": {
			"nonce": 0,
			"balance": 44
		},
		"198d6e08b28e813feb01e4a400839b85e18080ce": {
			"nonce": 10,
			"balance": 42
		},
		"4aa02109374edfd260c0d3d03cb501c8d65457a9": {
			"nonce": 2,
			"balance": 15
		}
	}
}`
	require.Equal(s.T(), want, got)
}

func (s *ProcessorStateSuite) TestTransactionProcessor_Multilayer() {
	testCycles := 100
	maxTransactions := 20
	minTransactions := 1
	maxAmount := uint64(1000)
	requiredBalance := int64(int(maxAmount) * testCycles * maxTransactions)

	lg := logtest.New(s.T())
	txDb := database.NewMemDatabase()
	db := database.NewMemDatabase()
	processor := NewTransactionProcessor(db, txDb, lg)

	revertToLayer := rand.Intn(testCycles)
	revertAfterLayer := rand.Intn(testCycles - revertToLayer) // rand.Intn(min(testCycles - revertToLayer,maxPas.processors))

	signers := []*signing.EdSigner{
		signing.NewEdSigner(),
		signing.NewEdSigner(),
		signing.NewEdSigner(),
	}
	accounts := []*Object{
		createAccount(processor, toAddr(signers[0].PublicKey().Bytes()), requiredBalance, 0),
		createAccount(processor, toAddr(signers[1].PublicKey().Bytes()), requiredBalance, 10),
		createAccount(processor, toAddr(signers[2].PublicKey().Bytes()), requiredBalance, 0),
	}
	processor.Commit()

	var want string
	for i := 0; i < testCycles; i++ {
		numOfTransactions := rand.Intn(maxTransactions-minTransactions) + minTransactions
		var trns []*types.Transaction
		nonceTrack := make(map[*Object]int)
		for j := 0; j < numOfTransactions; j++ {
			src := int(rand.Uint32() % (uint32(len(accounts) - 1)))
			srcAccount := accounts[src]
			dstAccount := accounts[int(rand.Uint32()%(uint32(len(accounts)-1)))]

			if _, ok := nonceTrack[srcAccount]; !ok {
				nonceTrack[srcAccount] = 0
			} else {
				nonceTrack[srcAccount]++
			}

			for dstAccount == srcAccount {
				dstAccount = accounts[int(rand.Uint32()%(uint32(len(accounts)-1)))]
			}

			if processor.GetNonce(srcAccount.address)+uint64(nonceTrack[srcAccount]) == 0 {
				trns = append(trns, createSpawnTransaction(dstAccount.address, signers[src]))
				nonceTrack[srcAccount]++
			}

			t := createCallTransaction(s.T(),
				processor.GetNonce(srcAccount.address)+uint64(nonceTrack[srcAccount]),
				dstAccount.address,
				rand.Uint64()%maxAmount,
				5, signers[src])
			trns = append(trns, t)
		}
		failed, err := processor.ApplyTransactions(types.NewLayerID(uint32(i)), trns)

		s.Require().NoError(err)
		s.Require().Zero(failed)

		if i == revertToLayer {
			want = string(processor.Dump())
		}

		if i == revertToLayer+revertAfterLayer {
			err = processor.LoadState(types.NewLayerID(uint32(revertToLayer)))
			s.Require().NoError(err)
			got := string(processor.Dump())
			s.Require().Equal(want, got)
		}
	}
}

func newTx(t *testing.T, nonce, totalAmount uint64, signer *signing.EdSigner) *types.Transaction {
	feeAmount := uint64(1)
	rec := types.Address{byte(rand.Int()), byte(rand.Int()), byte(rand.Int()), byte(rand.Int())}
	return createCallTransaction(t, nonce, rec, totalAmount-feeAmount, feeAmount, signer)
}

func TestTransactionProcessor_ApplyTransactionTestSuite(t *testing.T) {
	suite.Run(t, new(ProcessorStateSuite))
}

func createSignerTransaction(t *testing.T, key ed25519.PrivateKey) *types.Transaction {
	t.Helper()
	r := require.New(t)
	signer, err := signing.NewEdSignerFromBuffer(key)
	r.NoError(err)
	tx, err := transaction.GenerateCallTransaction(signer, toAddr([]byte{0xde}), 1111, 123, 11, 456)
	r.NoError(err)
	return tx
}

func TestValidateTxSignature(t *testing.T) {
	db := database.NewMemDatabase()
	lg := logtest.New(t).WithName("proc_logger")
	proc := NewTransactionProcessor(db, appliedTxsMock{}, lg)

	// positive flow
	pub, pri, _ := ed25519.GenerateKey(crand.Reader)
	createAccount(proc, types.GenerateAddress(pub), 123, 321)
	tx := createSignerTransaction(t, pri)

	assert.Equal(t, types.GenerateAddress(pub), tx.Origin())
	assert.True(t, proc.AddressExists(tx.Origin()))

	// negative flow
	pub, pri, _ = ed25519.GenerateKey(crand.Reader)
	tx = createSignerTransaction(t, pri)

	assert.False(t, proc.AddressExists(tx.Origin()))
	assert.Equal(t, types.GenerateAddress(pub), tx.Origin())
}

func TestTransactionProcessor_GetStateRoot(t *testing.T) {
	r := require.New(t)

	db := database.NewMemDatabase()
	lg := logtest.New(t).WithName("proc_logger")
	proc := NewTransactionProcessor(db, appliedTxsMock{}, lg)

	r.NotEqual(types.Hash32{}, proc.rootHash)

	expectedRoot := types.Hash32{1, 2, 3}
	r.NoError(proc.saveStateRoot(expectedRoot, types.NewLayerID(1)))

	actualRoot := proc.GetStateRoot()
	r.Equal(expectedRoot, actualRoot)
}

func TestTransactionProcessor_ApplyTransactions(t *testing.T) {
	lg := logtest.New(t).WithName("proc_logger")
	db := database.NewMemDatabase()
	processor := NewTransactionProcessor(db, db, lg)

	signerBuf := []byte("22222222222222222222222222222222")
	signerBuf = append(signerBuf, []byte{
		94, 33, 44, 9, 128, 228, 179, 159, 192, 151, 33, 19, 74, 160, 33, 9,
		55, 78, 223, 210, 96, 192, 211, 208, 60, 181, 1, 200, 214, 84, 87, 169,
	}...)
	signer, err := signing.NewEdSignerFromBuffer(signerBuf)
	assert.NoError(t, err)
	obj1 := createAccount(processor, SignerToAddr(signer), 21, 0)
	obj2 := createAccount(processor, toAddr([]byte{0x01, 0o2}), 1, 10)
	createAccount(processor, toAddr([]byte{0x02}), 44, 0)
	_, err = processor.Commit()
	assert.NoError(t, err)

	transactions := []*types.Transaction{
		createSpawnTransaction(obj2.address, signer),
		createCallTransaction(t, obj1.Nonce(), obj2.address, 1, 5, signer),
	}

	_, err = processor.ApplyTransactions(types.NewLayerID(1), transactions)
	assert.NoError(t, err)

	_, err = processor.ApplyTransactions(types.NewLayerID(2), []*types.Transaction{})
	assert.NoError(t, err)

	_, err = processor.ApplyTransactions(types.NewLayerID(3), []*types.Transaction{})
	assert.NoError(t, err)

	_, err = processor.GetLayerStateRoot(types.NewLayerID(3))
	assert.NoError(t, err)
}
