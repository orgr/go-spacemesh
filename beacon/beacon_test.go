package beacon

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spacemeshos/go-spacemesh/beacon/mocks"
	"github.com/spacemeshos/go-spacemesh/beacon/weakcoin"
	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/common/util"
	"github.com/spacemeshos/go-spacemesh/log/logtest"
	"github.com/spacemeshos/go-spacemesh/p2p/pubsub"
	pubsubmocks "github.com/spacemeshos/go-spacemesh/p2p/pubsub/mocks"
	"github.com/spacemeshos/go-spacemesh/signing"
	"github.com/spacemeshos/go-spacemesh/sql"
	smocks "github.com/spacemeshos/go-spacemesh/system/mocks"
)

func coinValueMock(tb testing.TB, value bool) coin {
	ctrl := gomock.NewController(tb)
	defer ctrl.Finish()

	coinMock := mocks.NewMockcoin(ctrl)
	coinMock.EXPECT().StartEpoch(
		gomock.Any(),
		gomock.AssignableToTypeOf(types.EpochID(0)),
		gomock.AssignableToTypeOf(weakcoin.UnitAllowances{}),
	).AnyTimes()
	coinMock.EXPECT().FinishEpoch(gomock.Any(), gomock.AssignableToTypeOf(types.EpochID(0))).AnyTimes()
	coinMock.EXPECT().StartRound(gomock.Any(), gomock.AssignableToTypeOf(types.RoundID(0))).
		AnyTimes().Return(nil)
	coinMock.EXPECT().FinishRound(gomock.Any()).AnyTimes()
	coinMock.EXPECT().Get(
		gomock.Any(),
		gomock.AssignableToTypeOf(types.EpochID(0)),
		gomock.AssignableToTypeOf(types.RoundID(0)),
	).AnyTimes().Return(value)
	return coinMock
}

func newPublisher(tb testing.TB) pubsub.Publisher {
	tb.Helper()
	ctrl := gomock.NewController(tb)
	defer ctrl.Finish()

	publisher := pubsubmocks.NewMockPublisher(ctrl)
	publisher.EXPECT().
		Publish(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).
		AnyTimes()
	return publisher
}

type testProtocolDriver struct {
	*ProtocolDriver
	ctrl   *gomock.Controller
	mAtxDB *mocks.MockactivationDB
	mClock *mocks.MocklayerClock
	mSync  *smocks.MockSyncStateProvider
}

func setUpProtocolDriver(t *testing.T) *testProtocolDriver {
	types.SetLayersPerEpoch(3)
	ctrl := gomock.NewController(t)
	tpd := &testProtocolDriver{
		ctrl:   ctrl,
		mAtxDB: mocks.NewMockactivationDB(ctrl),
		mClock: mocks.NewMocklayerClock(ctrl),
		mSync:  smocks.NewMockSyncStateProvider(ctrl),
	}
	edSgn := signing.NewEdSigner()
	edPubkey := edSgn.PublicKey()
	vrfSigner, vrfPub, err := signing.NewVRFSigner(edSgn.Sign(edPubkey.Bytes()))
	require.NoError(t, err)
	minerID := types.NodeID{Key: edPubkey.String(), VRFPublicKey: vrfPub}
	tpd.ProtocolDriver = New(minerID, newPublisher(t), tpd.mAtxDB, edSgn, vrfSigner, sql.InMemory(), tpd.mClock,
		WithConfig(UnitTestConfig()),
		WithLogger(logtest.New(t).WithName("Beacon")),
		withWeakCoin(coinValueMock(t, true)))
	tpd.ProtocolDriver.SetSyncState(tpd.mSync)
	tpd.ProtocolDriver.setMetricsRegistry(prometheus.NewPedanticRegistry())
	return tpd
}

func TestBeacon(t *testing.T) {
	tpd := setUpProtocolDriver(t)
	tpd.mSync.EXPECT().IsSynced(gomock.Any()).Return(true).AnyTimes()

	tpd.mClock.EXPECT().Subscribe().Times(1)
	tpd.mClock.EXPECT().GetCurrentLayer().Return(types.NewLayerID(1)).Times(1)
	tpd.mClock.EXPECT().LayerToTime(types.EpochID(1).FirstLayer()).Return(time.Now()).Times(1)
	tpd.Start(context.TODO())

	tpd.onNewEpoch(context.TODO(), types.EpochID(0))
	tpd.onNewEpoch(context.TODO(), types.EpochID(1))
	tpd.mAtxDB.EXPECT().GetNodeAtxIDForEpoch(gomock.Any(), gomock.Any()).Return(types.ATXID{}, nil).Times(1)
	tpd.mAtxDB.EXPECT().GetEpochWeight(gomock.Any()).Return(uint64(10), nil, nil).Times(1)
	tpd.onNewEpoch(context.TODO(), types.EpochID(2))

	got, err := tpd.GetBeacon(types.EpochID(2))
	require.NoError(t, err)
	assert.EqualValues(t, got, types.HexToBeacon(types.BootstrapBeacon))

	got, err = tpd.GetBeacon(types.EpochID(3))
	require.NoError(t, err)
	expected := types.HexToBeacon("0xe3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	assert.EqualValues(t, expected, got)

	tpd.mClock.EXPECT().Unsubscribe(gomock.Any()).Times(1)
	tpd.Close()
}

func TestBeaconZeroWeightEpoch(t *testing.T) {
	tpd := setUpProtocolDriver(t)
	tpd.mSync.EXPECT().IsSynced(gomock.Any()).Return(true).AnyTimes()

	tpd.mClock.EXPECT().Subscribe().Times(1)
	tpd.mClock.EXPECT().GetCurrentLayer().Return(types.NewLayerID(1)).Times(1)
	tpd.mClock.EXPECT().LayerToTime(types.EpochID(1).FirstLayer()).Return(time.Now()).Times(1)
	tpd.Start(context.TODO())

	tpd.onNewEpoch(context.TODO(), types.EpochID(0))
	tpd.onNewEpoch(context.TODO(), types.EpochID(1))
	tpd.mAtxDB.EXPECT().GetNodeAtxIDForEpoch(gomock.Any(), gomock.Any()).Return(types.ATXID{}, nil).Times(1)
	tpd.mAtxDB.EXPECT().GetEpochWeight(gomock.Any()).Return(uint64(0), nil, nil).Times(1)
	tpd.onNewEpoch(context.TODO(), types.EpochID(2))

	got, err := tpd.GetBeacon(types.EpochID(2))
	require.NoError(t, err)
	assert.EqualValues(t, got, types.HexToBeacon(types.BootstrapBeacon))

	got, err = tpd.GetBeacon(types.EpochID(3))
	assert.Equal(t, errBeaconNotCalculated, err)
	assert.Equal(t, types.EmptyBeacon, got)

	tpd.mClock.EXPECT().Unsubscribe(gomock.Any()).Times(1)
	tpd.Close()
}

func TestBeaconNotSynced(t *testing.T) {
	tpd := setUpProtocolDriver(t)
	tpd.mSync.EXPECT().IsSynced(gomock.Any()).Return(false).AnyTimes()

	tpd.mClock.EXPECT().Subscribe().Times(1)
	tpd.mClock.EXPECT().GetCurrentLayer().Return(types.NewLayerID(1)).Times(1)
	tpd.mClock.EXPECT().LayerToTime(types.EpochID(1).FirstLayer()).Return(time.Now()).Times(1)
	tpd.Start(context.TODO())

	tpd.onNewEpoch(context.TODO(), types.EpochID(0))
	tpd.onNewEpoch(context.TODO(), types.EpochID(1))
	tpd.onNewEpoch(context.TODO(), types.EpochID(2))

	got, err := tpd.GetBeacon(types.EpochID(2))
	require.NoError(t, err)
	assert.EqualValues(t, got, types.HexToBeacon(types.BootstrapBeacon))

	got, err = tpd.GetBeacon(types.EpochID(3))
	assert.Equal(t, errBeaconNotCalculated, err)
	assert.Equal(t, types.EmptyBeacon, got)

	tpd.mClock.EXPECT().Unsubscribe(gomock.Any()).Times(1)
	tpd.Close()
}

func TestBeaconNoATXInPreviousEpoch(t *testing.T) {
	tpd := setUpProtocolDriver(t)
	tpd.mSync.EXPECT().IsSynced(gomock.Any()).Return(true).AnyTimes()

	tpd.mClock.EXPECT().Subscribe().Times(1)
	tpd.mClock.EXPECT().GetCurrentLayer().Return(types.NewLayerID(1)).Times(1)
	tpd.mClock.EXPECT().LayerToTime(types.EpochID(1).FirstLayer()).Return(time.Now()).Times(1)
	tpd.Start(context.TODO())

	tpd.onNewEpoch(context.TODO(), types.EpochID(0))
	tpd.onNewEpoch(context.TODO(), types.EpochID(1))
	tpd.mAtxDB.EXPECT().GetNodeAtxIDForEpoch(gomock.Any(), gomock.Any()).Return(types.ATXID{}, sql.ErrNotFound).Times(1)
	tpd.onNewEpoch(context.TODO(), types.EpochID(2))

	got, err := tpd.GetBeacon(types.EpochID(2))
	require.NoError(t, err)
	assert.EqualValues(t, got, types.HexToBeacon(types.BootstrapBeacon))

	got, err = tpd.GetBeacon(types.EpochID(3))
	assert.Equal(t, errBeaconNotCalculated, err)
	assert.Equal(t, types.EmptyBeacon, got)

	tpd.mClock.EXPECT().Unsubscribe(gomock.Any()).Times(1)
	tpd.Close()
}

func TestBeaconWithMetrics(t *testing.T) {
	tpd := setUpProtocolDriver(t)
	tpd.mSync.EXPECT().IsSynced(gomock.Any()).Return(true).AnyTimes()

	tpd.mAtxDB.EXPECT().GetNodeAtxIDForEpoch(gomock.Any(), gomock.Any()).Return(types.ATXID{}, nil).AnyTimes()
	tpd.mAtxDB.EXPECT().GetEpochWeight(gomock.Any()).Return(uint64(10000), nil, nil).AnyTimes()
	atxHeader := &types.ActivationTxHeader{
		NIPostChallenge: types.NIPostChallenge{
			StartTick: 0,
			EndTick:   1,
		},
		NumUnits: 199,
	}
	tpd.mAtxDB.EXPECT().GetAtxHeader(gomock.Any()).Return(atxHeader, nil).AnyTimes()
	tpd.mAtxDB.EXPECT().GetNodeAtxIDForEpoch(gomock.Any(), gomock.Any()).Return(types.ATXID{}, nil).AnyTimes()

	gLayer := types.GetEffectiveGenesis()
	tpd.mClock.EXPECT().Subscribe().Times(1)
	tpd.mClock.EXPECT().GetCurrentLayer().Return(gLayer).Times(1)
	tpd.mClock.EXPECT().LayerToTime((gLayer.GetEpoch() + 1).FirstLayer()).Return(time.Now()).Times(1)
	tpd.Start(context.TODO())

	epoch3Beacon := types.HexToBeacon("0xe3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	epoch := types.EpochID(3)
	finalLayer := types.NewLayerID(types.GetLayersPerEpoch() * uint32(epoch))
	beacon1 := types.RandomBeacon()
	beacon2 := types.RandomBeacon()
	for layer := gLayer.Add(1); layer.Before(finalLayer); layer = layer.Add(1) {
		tpd.mClock.EXPECT().GetCurrentLayer().Return(layer).AnyTimes()
		if layer.FirstInEpoch() {
			tpd.onNewEpoch(context.TODO(), layer.GetEpoch())
		}
		thisEpoch := layer.GetEpoch()
		tpd.recordBeacon(thisEpoch, types.RandomBallotID(), beacon1, 100)
		tpd.recordBeacon(thisEpoch, types.RandomBallotID(), beacon2, 200)

		numCalculated := 0
		numObserved := 0
		numObservedWeight := 0
		allMetrics, err := prometheus.DefaultGatherer.Gather()
		assert.NoError(t, err)
		for _, m := range allMetrics {
			switch *m.Name {
			case "spacemesh_beacons_beacon_calculated_weight":
				require.Equal(t, 1, len(m.Metric))
				numCalculated++
				beaconStr := epoch3Beacon.ShortString()
				expected := fmt.Sprintf("label:<name:\"beacon\" value:\"%s\" > label:<name:\"epoch\" value:\"%d\" > counter:<value:%d > ", beaconStr, thisEpoch+1, atxHeader.GetWeight())
				assert.Equal(t, expected, m.Metric[0].String())
			case "spacemesh_beacons_beacon_observed_total":
				require.Equal(t, 2, len(m.Metric))
				numObserved = numObserved + 2
				count := layer.OrdinalInEpoch() + 1
				expected := []string{
					fmt.Sprintf("label:<name:\"beacon\" value:\"%s\" > label:<name:\"epoch\" value:\"%d\" > counter:<value:%d > ", beacon1.ShortString(), thisEpoch, count),
					fmt.Sprintf("label:<name:\"beacon\" value:\"%s\" > label:<name:\"epoch\" value:\"%d\" > counter:<value:%d > ", beacon2.ShortString(), thisEpoch, count),
				}
				for _, subM := range m.Metric {
					assert.Contains(t, expected, subM.String())
				}
			case "spacemesh_beacons_beacon_observed_weight":
				require.Equal(t, 2, len(m.Metric))
				numObservedWeight = numObservedWeight + 2
				weight := (layer.OrdinalInEpoch() + 1) * 100
				expected := []string{
					fmt.Sprintf("label:<name:\"beacon\" value:\"%s\" > label:<name:\"epoch\" value:\"%d\" > counter:<value:%d > ", beacon1.ShortString(), thisEpoch, weight),
					fmt.Sprintf("label:<name:\"beacon\" value:\"%s\" > label:<name:\"epoch\" value:\"%d\" > counter:<value:%d > ", beacon2.ShortString(), thisEpoch, weight*2),
				}
				for _, subM := range m.Metric {
					assert.Contains(t, expected, subM.String())
				}
			}
		}
		if layer.OrdinalInEpoch() == 2 {
			// there should be calculated beacon already by the last layer of the epoch
			assert.Equal(t, 1, numCalculated, layer)
		} else {
			assert.LessOrEqual(t, numCalculated, 1, layer)
		}
		assert.Equal(t, 2, numObserved, layer)
		assert.Equal(t, 2, numObservedWeight, layer)
	}

	tpd.mClock.EXPECT().Unsubscribe(gomock.Any()).Times(1)
	tpd.Close()
}

func TestBeacon_BeaconsWithDatabase(t *testing.T) {
	t.Parallel()

	pd := &ProtocolDriver{
		logger:  logtest.New(t).WithName("Beacon"),
		beacons: make(map[types.EpochID]types.Beacon),
		db:      sql.InMemory(),
	}
	epoch3 := types.EpochID(3)
	beacon2 := types.RandomBeacon()
	epoch5 := types.EpochID(5)
	beacon4 := types.RandomBeacon()
	err := pd.setBeacon(epoch3, beacon2)
	require.NoError(t, err)
	err = pd.setBeacon(epoch5, beacon4)
	require.NoError(t, err)

	got, err := pd.GetBeacon(epoch3)
	assert.NoError(t, err)
	assert.Equal(t, beacon2, got)

	got, err = pd.GetBeacon(epoch5)
	assert.NoError(t, err)
	assert.Equal(t, beacon4, got)

	got, err = pd.GetBeacon(epoch5 - 1)
	assert.Equal(t, errBeaconNotCalculated, err)
	assert.Equal(t, types.EmptyBeacon, got)

	// clear out the in-memory map
	// the database should still give us values
	pd.mu.Lock()
	pd.beacons = make(map[types.EpochID]types.Beacon)
	pd.mu.Unlock()

	got, err = pd.GetBeacon(epoch3)
	assert.NoError(t, err)
	assert.Equal(t, beacon2, got)

	got, err = pd.GetBeacon(epoch5)
	assert.NoError(t, err)
	assert.Equal(t, beacon4, got)

	got, err = pd.GetBeacon(epoch5 - 1)
	assert.Equal(t, errBeaconNotCalculated, err)
	assert.Equal(t, types.EmptyBeacon, got)
}

func TestBeacon_BeaconsWithDatabaseFailure(t *testing.T) {
	t.Parallel()

	pd := &ProtocolDriver{
		logger:  logtest.New(t).WithName("Beacon"),
		beacons: make(map[types.EpochID]types.Beacon),
		db:      sql.InMemory(),
	}
	epoch := types.EpochID(3)

	got, errGet := pd.getPersistedBeacon(epoch)
	assert.Equal(t, types.EmptyBeacon, got)
	assert.ErrorIs(t, errGet, sql.ErrNotFound)
}

func TestBeacon_BeaconsCleanupOldEpoch(t *testing.T) {
	t.Parallel()

	pd := &ProtocolDriver{
		logger:             logtest.New(t).WithName("Beacon"),
		db:                 sql.InMemory(),
		beacons:            make(map[types.EpochID]types.Beacon),
		beaconsFromBallots: make(map[types.EpochID]map[types.Beacon]*ballotWeight),
	}

	epoch := types.EpochID(5)
	for i := 0; i < numEpochsToKeep; i++ {
		e := epoch + types.EpochID(i)
		err := pd.setBeacon(e, types.RandomBeacon())
		require.NoError(t, err)
		pd.recordBeacon(e, types.RandomBallotID(), types.RandomBeacon(), 10)
		pd.cleanupEpoch(e)
		assert.Equal(t, i+1, len(pd.beacons))
		assert.Equal(t, i+1, len(pd.beaconsFromBallots))
	}
	assert.Equal(t, numEpochsToKeep, len(pd.beacons))
	assert.Equal(t, numEpochsToKeep, len(pd.beaconsFromBallots))

	epoch = epoch + numEpochsToKeep
	err := pd.setBeacon(epoch, types.RandomBeacon())
	require.NoError(t, err)
	pd.recordBeacon(epoch, types.RandomBallotID(), types.RandomBeacon(), 10)
	assert.Equal(t, numEpochsToKeep+1, len(pd.beacons))
	assert.Equal(t, numEpochsToKeep+1, len(pd.beaconsFromBallots))
	pd.cleanupEpoch(epoch)
	assert.Equal(t, numEpochsToKeep, len(pd.beacons))
	assert.Equal(t, numEpochsToKeep, len(pd.beaconsFromBallots))
}

func TestBeacon_ReportBeaconFromBallot(t *testing.T) {
	t.Parallel()

	types.SetLayersPerEpoch(3)
	pd := &ProtocolDriver{
		logger:             logtest.New(t).WithName("Beacon"),
		config:             UnitTestConfig(),
		db:                 sql.InMemory(),
		beacons:            make(map[types.EpochID]types.Beacon),
		beaconsFromBallots: make(map[types.EpochID]map[types.Beacon]*ballotWeight),
	}
	pd.config.BeaconSyncNumBallots = 3

	epoch := types.EpochID(3)
	beacon1 := types.RandomBeacon()
	beacon2 := types.RandomBeacon()
	pd.ReportBeaconFromBallot(epoch, types.RandomBallotID(), beacon1, 100)
	got, err := pd.GetBeacon(epoch)
	require.Equal(t, errBeaconNotCalculated, err)
	require.Equal(t, types.EmptyBeacon, got)
	pd.ReportBeaconFromBallot(epoch, types.RandomBallotID(), beacon2, 100)
	got, err = pd.GetBeacon(epoch)
	require.Equal(t, errBeaconNotCalculated, err)
	require.Equal(t, types.EmptyBeacon, got)
	pd.ReportBeaconFromBallot(epoch, types.RandomBallotID(), beacon1, 1)
	got, err = pd.GetBeacon(epoch)
	assert.NoError(t, err)
	assert.Equal(t, beacon1, got)
}

func TestBeacon_ReportBeaconFromBallot_SameBallot(t *testing.T) {
	t.Parallel()

	types.SetLayersPerEpoch(3)
	pd := &ProtocolDriver{
		logger:             logtest.New(t).WithName("Beacon"),
		config:             UnitTestConfig(),
		db:                 sql.InMemory(),
		beacons:            make(map[types.EpochID]types.Beacon),
		beaconsFromBallots: make(map[types.EpochID]map[types.Beacon]*ballotWeight),
	}
	pd.config.BeaconSyncNumBallots = 2

	epoch := types.EpochID(3)
	beacon1 := types.RandomBeacon()
	beacon2 := types.RandomBeacon()
	ballotID1 := types.RandomBallotID()
	ballotID2 := types.RandomBallotID()
	pd.ReportBeaconFromBallot(epoch, ballotID1, beacon1, 100)
	pd.ReportBeaconFromBallot(epoch, ballotID1, beacon1, 200)
	// same ballotID does not count twice
	got, err := pd.GetBeacon(epoch)
	require.Equal(t, errBeaconNotCalculated, err)
	require.Equal(t, types.EmptyBeacon, got)

	pd.ReportBeaconFromBallot(epoch, ballotID2, beacon2, 101)
	got, err = pd.GetBeacon(epoch)
	assert.NoError(t, err)
	assert.Equal(t, beacon2, got)
}

func TestBeacon_ensureEpochHasBeacon_BeaconAlreadyCalculated(t *testing.T) {
	t.Parallel()

	epoch := types.EpochID(3)
	beacon := types.RandomBeacon()
	beaconFromBallots := types.RandomBeacon()
	pd := &ProtocolDriver{
		logger: logtest.New(t).WithName("Beacon"),
		config: UnitTestConfig(),
		beacons: map[types.EpochID]types.Beacon{
			epoch: beacon,
		},
		beaconsFromBallots: make(map[types.EpochID]map[types.Beacon]*ballotWeight),
	}
	pd.config.BeaconSyncNumBallots = 2

	got, err := pd.GetBeacon(epoch)
	require.NoError(t, err)
	require.Equal(t, beacon, got)

	pd.ReportBeaconFromBallot(epoch, types.RandomBallotID(), beaconFromBallots, 100)
	pd.ReportBeaconFromBallot(epoch, types.RandomBallotID(), beaconFromBallots, 200)

	// should not change the beacon value
	got, err = pd.GetBeacon(epoch)
	require.NoError(t, err)
	require.Equal(t, beacon, got)
}

func TestBeacon_findMostWeightedBeaconForEpoch(t *testing.T) {
	t.Parallel()

	types.SetLayersPerEpoch(3)
	beacon1 := types.RandomBeacon()
	beacon2 := types.RandomBeacon()
	beacon3 := types.RandomBeacon()

	beaconsFromBlocks := map[types.Beacon]*ballotWeight{
		beacon1: {
			ballots: map[types.BallotID]struct{}{types.RandomBallotID(): {}, types.RandomBallotID(): {}},
			weight:  200,
		},
		beacon2: {
			ballots: map[types.BallotID]struct{}{types.RandomBallotID(): {}},
			weight:  201,
		},
		beacon3: {
			ballots: map[types.BallotID]struct{}{types.RandomBallotID(): {}},
			weight:  200,
		},
	}
	epoch := types.EpochID(3)
	pd := &ProtocolDriver{
		logger:             logtest.New(t).WithName("Beacon"),
		config:             UnitTestConfig(),
		beacons:            make(map[types.EpochID]types.Beacon),
		beaconsFromBallots: map[types.EpochID]map[types.Beacon]*ballotWeight{epoch: beaconsFromBlocks},
	}
	pd.config.BeaconSyncNumBallots = 2
	got := pd.findMostWeightedBeaconForEpoch(epoch)
	assert.Equal(t, beacon2, got)
}

func TestBeacon_findMostWeightedBeaconForEpoch_NotEnoughBlocks(t *testing.T) {
	t.Parallel()

	types.SetLayersPerEpoch(3)
	beacon1 := types.RandomBeacon()
	beacon2 := types.RandomBeacon()
	beacon3 := types.RandomBeacon()

	beaconsFromBlocks := map[types.Beacon]*ballotWeight{
		beacon1: {
			ballots: map[types.BallotID]struct{}{types.RandomBallotID(): {}, types.RandomBallotID(): {}},
			weight:  200,
		},
		beacon2: {
			ballots: map[types.BallotID]struct{}{types.RandomBallotID(): {}},
			weight:  201,
		},
		beacon3: {
			ballots: map[types.BallotID]struct{}{types.RandomBallotID(): {}},
			weight:  200,
		},
	}
	epoch := types.EpochID(3)
	pd := &ProtocolDriver{
		logger:             logtest.New(t).WithName("Beacon"),
		config:             UnitTestConfig(),
		beacons:            make(map[types.EpochID]types.Beacon),
		beaconsFromBallots: map[types.EpochID]map[types.Beacon]*ballotWeight{epoch: beaconsFromBlocks},
	}
	pd.config.BeaconSyncNumBallots = 5
	got := pd.findMostWeightedBeaconForEpoch(epoch)
	assert.Equal(t, types.EmptyBeacon, got)
}

func TestBeacon_findMostWeightedBeaconForEpoch_NoBeacon(t *testing.T) {
	t.Parallel()

	types.SetLayersPerEpoch(3)
	pd := &ProtocolDriver{
		logger:             logtest.New(t).WithName("Beacon"),
		config:             UnitTestConfig(),
		beacons:            make(map[types.EpochID]types.Beacon),
		beaconsFromBallots: make(map[types.EpochID]map[types.Beacon]*ballotWeight),
	}
	epoch := types.EpochID(3)
	got := pd.findMostWeightedBeaconForEpoch(epoch)
	assert.Equal(t, types.EmptyBeacon, got)
}

func TestBeacon_persistBeacon(t *testing.T) {
	tpd := setUpProtocolDriver(t)
	epoch := types.EpochID(5)
	beacon := types.RandomBeacon()
	require.NoError(t, tpd.setBeacon(epoch, beacon))

	// saving it again won't cause error
	assert.NoError(t, tpd.persistBeacon(epoch, beacon))
	// but saving a different one will
	assert.ErrorIs(t, tpd.persistBeacon(epoch, types.RandomBeacon()), errDifferentBeacon)
}

func TestBeacon_atxThresholdFraction(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	theta1, ok := new(big.Float).SetString("0.5")
	r.True(ok)

	theta2, ok := new(big.Float).SetString("0.0")
	r.True(ok)

	tt := []struct {
		name      string
		kappa     uint64
		q         *big.Rat
		w         uint64
		threshold *big.Float
	}{
		{
			name:      "with epoch weight",
			kappa:     40,
			q:         big.NewRat(1, 3),
			w:         60,
			threshold: theta1,
		},
		{
			name:      "zero epoch weight",
			kappa:     40,
			q:         big.NewRat(1, 3),
			w:         0,
			threshold: theta2,
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			threshold := atxThresholdFraction(tc.kappa, tc.q, tc.w)
			r.Equal(tc.threshold.String(), threshold.String())
		})
	}
}

func TestBeacon_atxThreshold(t *testing.T) {
	t.Parallel()

	r := require.New(t)
	tt := []struct {
		name      string
		kappa     uint64
		q         *big.Rat
		w         uint64
		threshold string
	}{
		{
			name:      "Case 1",
			kappa:     40,
			q:         big.NewRat(1, 3),
			w:         60,
			threshold: "0x80000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			name:      "Case 2",
			kappa:     400000,
			q:         big.NewRat(1, 3),
			w:         31744,
			threshold: "0xffffddbb63fcd30f0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			name:      "Case 3",
			kappa:     40,
			q:         new(big.Rat).SetFloat64(0.33),
			w:         60,
			threshold: "0x7f8ece00fe541f9f0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			threshold := atxThreshold(tc.kappa, tc.q, tc.w)
			r.EqualValues(tc.threshold, fmt.Sprintf("%#x", threshold))
		})
	}
}

func TestBeacon_proposalPassesEligibilityThreshold(t *testing.T) {
	t.Parallel()

	r := require.New(t)
	tt := []struct {
		name     string
		kappa    uint64
		q        *big.Rat
		w        uint64
		proposal []byte
		passes   bool
	}{
		{
			name:     "Case 1",
			kappa:    400000,
			q:        big.NewRat(1, 3),
			w:        31744,
			proposal: util.Hex2Bytes("cee191e87d83dc4fbd5e2d40679accf68237b1f09f73f16db5b05ae74b522def9d2ffee56eeb02070277be99a80666ffef9fd4514a51dc37419dd30a791e0f05"),
			passes:   true,
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := logtest.New(t).WithName("proposal checker")
			checker := createProposalChecker(logger, tc.kappa, tc.q, tc.w)
			passes := checker.IsProposalEligible(tc.proposal)
			r.EqualValues(tc.passes, passes)
		})
	}
}

func TestBeacon_buildProposal(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	tt := []struct {
		name   string
		epoch  types.EpochID
		result string
	}{
		{
			name:   "Case 1",
			epoch:  0x12345678,
			result: string(util.Hex2Bytes("000000024250000012345678")),
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := buildProposal(tc.epoch, logtest.New(t))
			r.Equal(tc.result, string(result))
		})
	}
}

func TestBeacon_signMessage(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	edSgn := signing.NewEdSigner()

	tt := []struct {
		name    string
		message interface{}
		result  []byte
	}{
		{
			name:    "Case 1",
			message: []byte{},
			result:  edSgn.Sign([]byte{0, 0, 0, 0}),
		},
		{
			name:    "Case 2",
			message: &struct{ Test int }{Test: 0x12345678},
			result:  edSgn.Sign([]byte{0x12, 0x34, 0x56, 0x78}),
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := signMessage(edSgn, tc.message, logtest.New(t))
			r.Equal(string(tc.result), string(result))
		})
	}
}

func TestBeacon_getSignedProposal(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	edSgn := signing.NewEdSigner()
	edPubkey := edSgn.PublicKey()

	vrfSigner, _, err := signing.NewVRFSigner(edSgn.Sign(edPubkey.Bytes()))
	r.NoError(err)

	tt := []struct {
		name   string
		epoch  types.EpochID
		result []byte
	}{
		{
			name:   "Case 1",
			epoch:  1,
			result: vrfSigner.Sign(util.Hex2Bytes("000000024250000000000001")),
		},
		{
			name:   "Case 2",
			epoch:  2,
			result: vrfSigner.Sign(util.Hex2Bytes("000000024250000000000002")),
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := buildSignedProposal(context.TODO(), vrfSigner, tc.epoch, logtest.New(t))
			r.Equal(string(tc.result), string(result))
		})
	}
}

func TestBeacon_signAndExtractED(t *testing.T) {
	r := require.New(t)

	signer := signing.NewEdSigner()
	verifier := signing.NewEDVerifier()

	message := []byte{1, 2, 3, 4}

	signature := signer.Sign(message)
	extractedPK, err := verifier.Extract(message, signature)
	r.NoError(err)

	ok := verifier.Verify(extractedPK, message, signature)

	r.Equal(signer.PublicKey().String(), extractedPK.String())
	r.True(ok)
}

func TestBeacon_signAndVerifyVRF(t *testing.T) {
	r := require.New(t)

	signer, _, err := signing.NewVRFSigner(bytes.Repeat([]byte{0x01}, 32))
	r.NoError(err)

	verifier := signing.VRFVerifier{}

	message := []byte{1, 2, 3, 4}

	signature := signer.Sign(message)
	ok := verifier.Verify(signer.PublicKey(), message, signature)
	r.True(ok)
}

func TestBeacon_calcBeacon(t *testing.T) {
	votes := allVotes{
		support: proposalSet{
			"0x1": {},
			"0x2": {},
			"0x4": {},
			"0x5": {},
		},
		against: proposalSet{
			"0x3": {},
			"0x6": {},
		},
	}
	beacon := calcBeacon(logtest.New(t), votes)
	expected := types.HexToBeacon("0x6d148de54cc5ac334cdf4537018209b0e9f5ea94c049417103065eac777ddb5c")
	assert.EqualValues(t, expected, beacon)
}
