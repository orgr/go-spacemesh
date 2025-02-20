// Package mesh defines the main store point for all the persisted mesh objects
// such as ATXs, ballots and blocks.
package mesh

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.uber.org/atomic"

	"github.com/spacemeshos/go-spacemesh/common/types"
	"github.com/spacemeshos/go-spacemesh/database"
	"github.com/spacemeshos/go-spacemesh/events"
	"github.com/spacemeshos/go-spacemesh/log"
	"github.com/spacemeshos/go-spacemesh/sql"
	"github.com/spacemeshos/go-spacemesh/sql/layers"
)

var errMissingHareOutput = errors.New("missing hare output")

// AtxDB holds logic for working with atxs.
type AtxDB interface {
	GetAtxHeader(id types.ATXID) (*types.ActivationTxHeader, error)
	GetFullAtx(id types.ATXID) (*types.ActivationTx, error)
	SyntacticallyValidateAtx(ctx context.Context, atx *types.ActivationTx) error
}

// Mesh is the logic layer above our mesh.DB database.
type Mesh struct {
	log.Log
	*DB
	AtxDB

	conState conservativeState
	trtl     tortoise

	mu sync.Mutex
	// latestLayer is the latest layer this node had seen from blocks
	latestLayer atomic.Value
	// latestLayerInState is the latest layer whose contents have been applied to the state
	latestLayerInState atomic.Value
	// processedLayer is the latest layer whose votes have been processed
	processedLayer atomic.Value
	// see doc for MissingLayer()
	missingLayer        atomic.Value
	nextProcessedLayers map[types.LayerID]struct{}
	maxProcessedLayer   types.LayerID

	// minUpdatedLayer is the earliest layer that have contextual validity updated.
	// since we optimistically apply blocks to state whenever hare terminates a layer,
	// if contextual validity changed for blocks in that layer, we need to
	// double-check whether we have applied the correct block for that layer.
	minUpdatedLayer atomic.Value
}

// NewMesh creates a new instant of a mesh.
func NewMesh(db *DB, atxDb AtxDB, trtl tortoise, state conservativeState, logger log.Log) *Mesh {
	msh := &Mesh{
		Log:                 logger,
		trtl:                trtl,
		conState:            state,
		DB:                  db,
		AtxDB:               atxDb,
		nextProcessedLayers: make(map[types.LayerID]struct{}),
	}
	msh.latestLayer.Store(types.GetEffectiveGenesis())
	msh.latestLayerInState.Store(types.GetEffectiveGenesis())
	msh.processedLayer.Store(types.LayerID{})
	msh.minUpdatedLayer.Store(types.LayerID{})

	gLyr := types.GetEffectiveGenesis()
	for i := types.NewLayerID(1); !i.After(gLyr); i = i.Add(1) {
		if i.Before(gLyr) {
			if err := msh.SetZeroBlockLayer(i); err != nil {
				msh.With().Panic("failed to set zero-block for genesis layer", i, log.Err(err))
			}
		}
		if err := layers.SetApplied(msh.db, i, types.EmptyBlockID); err != nil {
			msh.With().Panic("failed to set applied layer", i, log.Err(err))
		}
		if err := msh.persistLayerHashes(context.Background(), i, []types.BlockID{types.EmptyBlockID}); err != nil {
			msh.With().Panic("failed to persist hashes for layer", i, log.Err(err))
		}
		msh.setProcessedLayer(i)
	}
	return msh
}

// NewRecoveredMesh creates new instance of mesh with recovered mesh data fom database.
func NewRecoveredMesh(db *DB, atxDb AtxDB, trtl tortoise, state conservativeState, logger log.Log) *Mesh {
	msh := NewMesh(db, atxDb, trtl, state, logger)

	latest, err := msh.GetLatestLayer()
	if err != nil {
		logger.With().Panic("failed to recover latest layer", log.Err(err))
	}
	msh.setLatestLayer(latest)

	lyr, err := msh.GetProcessedLayer()
	if err != nil {
		logger.With().Panic("failed to recover processed layer", log.Err(err))
	}
	msh.setProcessedLayerFromRecoveredData(lyr)

	verified, err := msh.GetVerifiedLayer()
	if err != nil {
		logger.With().Panic("failed to recover latest verified layer", log.Err(err))
	}
	if err = msh.setLatestLayerInState(verified); err != nil {
		logger.With().Panic("failed to recover latest layer in state", log.Err(err))
	}

	_, err = state.RevertState(msh.LatestLayerInState())
	if err != nil {
		logger.With().Panic("failed to load state for layer", msh.LatestLayerInState(), log.Err(err))
	}

	msh.With().Info("recovered mesh from disk",
		log.FieldNamed("latest", msh.LatestLayer()),
		log.FieldNamed("processed", msh.ProcessedLayer()),
		log.String("root_hash", state.GetStateRoot().String()))

	return msh
}

func (msh *Mesh) resetMinUpdatedLayer(from types.LayerID) {
	if msh.minUpdatedLayer.CompareAndSwap(from, types.LayerID{}) {
		msh.With().Debug("min updated layer reset", log.Uint32("from", from.Uint32()))
	}
}

func (msh *Mesh) getMinUpdatedLayer() types.LayerID {
	value := msh.minUpdatedLayer.Load()
	if value == nil {
		return types.LayerID{}
	}
	return value.(types.LayerID)
}

// UpdateBlockValidity is the callback used when a block's contextual validity is updated by tortoise.
func (msh *Mesh) UpdateBlockValidity(bid types.BlockID, lid types.LayerID, newValid bool) error {
	msh.With().Debug("updating validity for block", lid, bid)
	oldValid, err := msh.DB.ContextualValidity(bid)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return fmt.Errorf("error reading contextual validity of block %v: %w", bid, err)
	}

	if oldValid != newValid {
		for {
			minUpdated := msh.getMinUpdatedLayer()
			if minUpdated != (types.LayerID{}) && !lid.Before(minUpdated) {
				break
			}
			if msh.minUpdatedLayer.CompareAndSwap(minUpdated, lid) {
				msh.With().Debug("min updated layer set for block", lid, bid)
				break
			}
		}
	}

	if err := msh.DB.SaveContextualValidity(bid, lid, newValid); err != nil {
		return err
	}
	return nil
}

// LatestLayerInState returns the latest layer we applied to state.
func (msh *Mesh) LatestLayerInState() types.LayerID {
	return msh.latestLayerInState.Load().(types.LayerID)
}

// LatestLayer - returns the latest layer we saw from the network.
func (msh *Mesh) LatestLayer() types.LayerID {
	return msh.latestLayer.Load().(types.LayerID)
}

// MissingLayer is a layer in (latestLayerInState, processLayer].
// this layer is missing critical data (valid blocks or transactions)
// and can't be applied to the state.
//
// First valid layer starts with 1. 0 is empty layer and can be ignored.
func (msh *Mesh) MissingLayer() types.LayerID {
	value := msh.missingLayer.Load()
	if value == nil {
		return types.LayerID{}
	}
	return value.(types.LayerID)
}

// setLatestLayer sets the latest layer we saw from the network.
func (msh *Mesh) setLatestLayer(lid types.LayerID) {
	events.ReportLayerUpdate(events.LayerUpdate{
		LayerID: lid,
		Status:  events.LayerStatusTypeUnknown,
	})
	for {
		current := msh.LatestLayer()
		if !lid.After(current) {
			return
		}
		if msh.latestLayer.CompareAndSwap(current, lid) {
			events.ReportNodeStatusUpdate()
			msh.With().Info("set latest known layer", lid)
			if err := layers.SetStatus(msh.db, lid, layers.Latest); err != nil {
				msh.Error("could not persist latest layer index")
			}
		}
	}
}

// GetLayer returns Layer i from the database.
func (msh *Mesh) GetLayer(lid types.LayerID) (*types.Layer, error) {
	ballots, err := msh.LayerBallots(lid)
	if err != nil {
		return nil, fmt.Errorf("layer ballots: %w", err)
	}
	blocks, err := msh.LayerBlocks(lid)
	if err != nil {
		return nil, fmt.Errorf("layer blocks: %w", err)
	}
	hash, err := layers.GetHash(msh.db, lid)
	if err != nil {
		hash = types.CalcBlockHash32Presorted(types.ToBlockIDs(blocks), nil)
	}
	return types.NewExistingLayer(lid, hash, ballots, blocks), nil
}

// GetLayerHash returns layer hash.
func (msh *Mesh) GetLayerHash(layerID types.LayerID) types.Hash32 {
	h, err := msh.recoverLayerHash(layerID)
	if err == nil {
		return h
	}
	if errors.Is(err, database.ErrNotFound) {
		// layer hash not persisted. i.e. contextual validity not yet determined
		lyr, err := msh.GetLayer(layerID)
		if err == nil {
			return lyr.Hash()
		}
	}
	return types.EmptyLayerHash
}

// ProcessedLayer returns the last processed layer ID.
func (msh *Mesh) ProcessedLayer() types.LayerID {
	return msh.processedLayer.Load().(types.LayerID)
}

func (msh *Mesh) setProcessedLayerFromRecoveredData(lid types.LayerID) {
	msh.processedLayer.Store(lid)
	msh.Event().Info("processed layer set from recovered data", lid)
}

func (msh *Mesh) setProcessedLayer(layerID types.LayerID) {
	processed := msh.ProcessedLayer()
	if !layerID.After(processed) {
		msh.With().Info("trying to set processed layer to an older layer",
			log.FieldNamed("processed", processed),
			layerID)
		return
	}

	if layerID.After(msh.maxProcessedLayer) {
		msh.maxProcessedLayer = layerID
	}

	if layerID != processed.Add(1) {
		msh.With().Info("trying to set processed layer out of order",
			log.FieldNamed("processed", processed),
			layerID)
		msh.nextProcessedLayers[layerID] = struct{}{}
		return
	}

	msh.nextProcessedLayers[layerID] = struct{}{}
	for i := layerID; !i.After(msh.maxProcessedLayer); i = i.Add(1) {
		_, ok := msh.nextProcessedLayers[i]
		if !ok {
			break
		}
		processed = i
		delete(msh.nextProcessedLayers, i)
	}
	msh.processedLayer.Store(processed)
	events.ReportNodeStatusUpdate()
	msh.Event().Info("processed layer set", processed)

	if err := msh.persistProcessedLayer(processed); err != nil {
		msh.With().Error("failed to persist processed layer",
			log.FieldNamed("processed", processed),
			log.Err(err))
	}
}

func (msh *Mesh) revertMaybe(ctx context.Context, logger log.Log, newVerified types.LayerID) error {
	minUpdated := msh.getMinUpdatedLayer()
	if minUpdated == (types.LayerID{}) {
		// no contextual validity update since the last ProcessLayer() call
		return nil
	}

	msh.resetMinUpdatedLayer(minUpdated)
	if minUpdated.After(msh.LatestLayerInState()) {
		// nothing to do
		return nil
	}

	var revertTo types.LayerID
	for lid := minUpdated; !lid.After(newVerified); lid = lid.Add(1) {
		valids, err := msh.layerValidBlocks(ctx, lid, newVerified)
		if err != nil {
			return err
		}

		bid := types.EmptyBlockID
		block := msh.getBlockToApply(valids)
		if block != nil {
			bid = block.ID()
		}
		applied, err := layers.GetApplied(msh.db, lid)
		if err != nil {
			return fmt.Errorf("get applied %v: %w", lid, err)
		}
		if bid != applied {
			revertTo = lid.Sub(1)
			break
		}
	}

	if revertTo == (types.LayerID{}) {
		// all the applied blocks are correct
		return nil
	}

	logger = logger.WithFields(log.Stringer("revert_to", revertTo))
	logger.Info("reverting state to layer")
	if err := msh.revertState(logger, revertTo); err != nil {
		logger.With().Error("failed to revert state",
			log.Uint32("revert_to", revertTo.Uint32()),
			log.Err(err))
		return err
	}
	if err := msh.setLatestLayerInState(revertTo); err != nil {
		return err
	}

	logger.With().Info("reverted state to layer", log.Uint32("revert_to", revertTo.Uint32()))
	return nil
}

// ProcessLayer performs fairly heavy lifting: it triggers tortoise to process the full contents of the layer (i.e.,
// all of its blocks), then to attempt to validate all unvalidated layers up to this layer. It also applies state for
// newly-validated layers.
func (msh *Mesh) ProcessLayer(ctx context.Context, layerID types.LayerID) error {
	msh.mu.Lock()
	defer msh.mu.Unlock()

	logger := msh.WithContext(ctx).WithFields(layerID)
	logger.Info("processing layer")

	// pass the layer to tortoise for processing
	newVerified := msh.trtl.HandleIncomingLayer(ctx, layerID)
	logger.With().Info("tortoise results", log.FieldNamed("verified", newVerified))

	// set processed layer even if later code will fail, as that failure is not related
	// to the layer that is being processed
	msh.setProcessedLayer(layerID)

	if err := msh.revertMaybe(ctx, logger, newVerified); err != nil {
		return err
	}

	// mesh can't skip layer that failed to complete
	from := msh.LatestLayerInState().Add(1)
	to := layerID
	if from == msh.MissingLayer() {
		to = msh.maxProcessedLayer
	}

	if !to.Before(from) {
		if err := msh.pushLayersToState(ctx, from, to, newVerified); err != nil {
			logger.With().Error("failed to push layers to state", log.Err(err))
			return err
		}
	}

	logger.Info("done processing layer")
	return nil
}

func (msh *Mesh) getAggregatedHash(lid types.LayerID) (types.Hash32, error) {
	if !lid.After(types.NewLayerID(1)) {
		return types.EmptyLayerHash, nil
	}
	return layers.GetAggregatedHash(msh.db, lid)
}

func (msh *Mesh) persistLayerHashes(ctx context.Context, lid types.LayerID, bids []types.BlockID) error {
	logger := msh.WithContext(ctx)
	logger.With().Debug("persisting layer hash", lid, log.Int("num_blocks", len(bids)))
	types.SortBlockIDs(bids)
	hash := types.CalcBlocksHash32(bids, nil)
	if err := msh.persistLayerHash(lid, hash); err != nil {
		logger.With().Error("failed to persist layer hash", lid, log.Err(err))
		return err
	}

	prevHash, err := msh.getAggregatedHash(lid.Sub(1))
	if err != nil {
		logger.With().Debug("failed to get previous aggregated hash", lid, log.Err(err))
		return err
	}

	logger.With().Debug("got previous aggregatedHash", lid, log.String("prevAggHash", prevHash.ShortString()))
	newAggHash := types.CalcBlocksHash32(bids, prevHash.Bytes())
	if err = msh.persistAggregatedLayerHash(lid, newAggHash); err != nil {
		logger.With().Error("failed to persist aggregated layer hash", lid, log.Err(err))
		return err
	}
	logger.With().Info("aggregated hash updated for layer",
		lid,
		log.String("hash", hash.ShortString()),
		log.String("agg_hash", newAggHash.ShortString()))
	return nil
}

// apply the state of a range of layers, including re-adding transactions from invalid blocks to the mempool.
func (msh *Mesh) pushLayersToState(ctx context.Context, from, to, latestVerified types.LayerID) error {
	logger := msh.WithContext(ctx).WithFields(
		log.Stringer("from_layer", from),
		log.Stringer("to_layer", to))
	logger.Info("pushing layers to state")
	if from.Before(types.GetEffectiveGenesis()) || to.Before(types.GetEffectiveGenesis()) {
		logger.Panic("tried to push genesis layers")
	}

	missing := msh.MissingLayer()
	// we never reapply the state of oldVerified. note that state reversions must be handled separately.
	for layerID := from; !layerID.After(to); layerID = layerID.Add(1) {
		if !layerID.After(msh.LatestLayerInState()) {
			logger.With().Error("trying to apply layer before currently applied layer",
				log.Stringer("applied_layer", msh.LatestLayerInState()),
				layerID,
			)
			continue
		}
		if err := msh.pushLayer(ctx, layerID, latestVerified); err != nil {
			msh.missingLayer.Store(layerID)
			return err
		}
		if layerID == missing {
			msh.missingLayer.Store(types.LayerID{})
		}
	}

	return nil
}

// TODO: change this per conclusion in https://community.spacemesh.io/t/new-history-reversal-attack-and-mitigation/268
func (msh *Mesh) getBlockToApply(validBlocks []*types.Block) *types.Block {
	if len(validBlocks) == 0 {
		return nil
	}
	sorted := types.SortBlocks(validBlocks)
	return sorted[0]
}

func (msh *Mesh) layerValidBlocks(ctx context.Context, layerID, latestVerified types.LayerID) ([]*types.Block, error) {
	blocks, err := msh.LayerBlocks(layerID)
	if err != nil {
		msh.WithContext(ctx).With().Error("failed to get layer blocks", layerID, log.Err(err))
		return nil, fmt.Errorf("failed to get layer blocks %s: %w", layerID, err)
	}
	var validBlocks []*types.Block

	if layerID.After(latestVerified) {
		// tortoise has not verified this layer yet, simply apply the block that hare certified
		bid, err := msh.DB.GetHareConsensusOutput(layerID)
		if err != nil {
			msh.WithContext(ctx).With().Error("failed to get hare output", layerID, log.Err(err))
			return nil, fmt.Errorf("%w: get hare output %v", errMissingHareOutput, err.Error())
		}
		// hare output an empty layer
		if bid == types.EmptyBlockID {
			return nil, nil
		}
		for _, b := range blocks {
			if b.ID() == bid {
				validBlocks = append(validBlocks, b)
				break
			}
		}
		return validBlocks, nil
	}

	for _, b := range blocks {
		valid, err := msh.DB.ContextualValidity(b.ID())
		// block contextual validity is determined by layer. if one block in the layer is not determined,
		// the whole layer is not yet verified.
		if err != nil {
			msh.With().Warning("block contextual validity not yet determined", b.ID(), b.LayerIndex, log.Err(err))
			return nil, fmt.Errorf("get block validity: %w", err)
		}
		if valid {
			validBlocks = append(validBlocks, b)
		}
	}
	return validBlocks, nil
}

func (msh *Mesh) pushLayer(ctx context.Context, layerID, latestVerified types.LayerID) error {
	valids, err := msh.layerValidBlocks(ctx, layerID, latestVerified)
	if err != nil {
		return err
	}
	toApply := msh.getBlockToApply(valids)

	if err = msh.updateStateWithLayer(ctx, layerID, toApply); err != nil {
		return fmt.Errorf("failed to update state %s: %w", layerID, err)
	}

	msh.Event().Info("end of layer state root",
		layerID,
		log.Stringer("state_root", msh.conState.GetStateRoot()),
	)

	if err = msh.persistLayerHashes(ctx, layerID, types.ToBlockIDs(valids)); err != nil {
		msh.With().Error("failed to persist layer hashes", layerID, log.Err(err))
		return err
	}
	return nil
}

// revertState reverts to state as of a previous layer.
func (msh *Mesh) revertState(logger log.Log, revertTo types.LayerID) error {
	root, err := msh.conState.RevertState(revertTo)
	if err != nil {
		return fmt.Errorf("revert state to layer %v: %w", revertTo, err)
	}
	logger.With().Info("successfully reverted state", log.Stringer("state_root", root))
	if err = layers.UnsetAppliedFrom(msh.db, revertTo.Add(1)); err != nil {
		logger.With().Error("failed to unset applied layer", log.Err(err))
		return fmt.Errorf("unset applied from layer %v: %w", revertTo.Add(1), err)
	}
	return nil
}

func (msh *Mesh) applyState(block *types.Block) error {
	failedTxs, err := msh.conState.ApplyLayer(block)
	if err != nil {
		msh.With().Error("failed to apply transactions",
			block.LayerIndex,
			log.Int("num_failed_txs", len(failedTxs)),
			log.Err(err))
		return fmt.Errorf("apply layer: %w", err)
	}

	if err := layers.SetApplied(msh.db, block.LayerIndex, block.ID()); err != nil {
		return fmt.Errorf("set applied block: %w", err)
	}

	if err := msh.DB.writeTransactionRewards(block.LayerIndex, block.Rewards); err != nil {
		msh.With().Error("cannot write reward to db", log.Err(err))
		return err
	}
	reportRewards(block)

	msh.With().Info("applied transactions",
		block.LayerIndex,
		log.Int("valid_block_txs", len(block.TxIDs)),
		log.Int("num_failed_txs", len(failedTxs)),
	)
	return nil
}

// ProcessLayerPerHareOutput receives hare output once it finishes running for a given layer.
func (msh *Mesh) ProcessLayerPerHareOutput(ctx context.Context, layerID types.LayerID, blockID types.BlockID) error {
	logger := msh.WithContext(ctx).WithFields(layerID, blockID)
	if blockID == types.EmptyBlockID {
		logger.Info("received empty set from hare")
	} else {
		// double-check we have this block in the mesh
		_, err := msh.DB.GetBlock(blockID)
		if err != nil {
			logger.With().Error("hare terminated with block that is not present in mesh", log.Err(err))
			return err
		}
	}
	// report that hare "approved" this layer
	events.ReportLayerUpdate(events.LayerUpdate{
		LayerID: layerID,
		Status:  events.LayerStatusTypeApproved,
	})

	logger.Info("saving hare output for layer")
	if err := msh.SaveHareConsensusOutput(ctx, layerID, blockID); err != nil {
		logger.With().Error("failed to save hare output", log.Err(err))
		return err
	}
	return msh.ProcessLayer(ctx, layerID)
}

// apply the state for a single layer.
func (msh *Mesh) updateStateWithLayer(ctx context.Context, layerID types.LayerID, block *types.Block) error {
	if latest := msh.LatestLayerInState(); layerID != latest.Add(1) {
		msh.WithContext(ctx).With().Panic("update state out-of-order",
			log.FieldNamed("verified", layerID),
			log.FieldNamed("latest", latest))
	}

	applied := types.EmptyBlockID
	if block != nil {
		if err := msh.applyState(block); err != nil {
			return err
		}
		applied = block.ID()
	}

	if err := layers.SetApplied(msh.db, layerID, applied); err != nil {
		return fmt.Errorf("set applied for %v/%v: %w", layerID, applied, err)
	}

	if err := msh.setLatestLayerInState(layerID); err != nil {
		return err
	}
	return nil
}

func (msh *Mesh) setLatestLayerInState(lyr types.LayerID) error {
	// Update validated layer only after applying transactions since loading of
	// state depends on processedLayer param.
	if err := layers.SetStatus(msh.db, lyr, layers.Applied); err != nil {
		// can happen if database already closed
		msh.Error("could not persist validated layer index %d: %v", lyr, err.Error())
		return fmt.Errorf("put into DB: %w", err)
	}
	msh.latestLayerInState.Store(lyr)
	return nil
}

// GetAggregatedLayerHash returns the aggregated layer hash up to the specified layer.
func (msh *Mesh) GetAggregatedLayerHash(layerID types.LayerID) types.Hash32 {
	h, err := layers.GetAggregatedHash(msh.db, layerID)
	if err != nil {
		return types.EmptyLayerHash
	}
	return h
}

var errLayerHasBlock = errors.New("layer has block")

// SetZeroBlockLayer tags lyr as a layer without blocks.
func (msh *Mesh) SetZeroBlockLayer(lyr types.LayerID) error {
	msh.With().Info("tagging zero block layer", lyr)
	// check database for layer
	if l, err := msh.GetLayer(lyr); err != nil {
		// database error
		if !errors.Is(err, sql.ErrNotFound) {
			msh.With().Error("error trying to fetch layer from database", lyr, log.Err(err))
			return err
		}
	} else if len(l.Blocks()) != 0 {
		// layer exists
		msh.With().Error("layer has blocks, cannot tag as zero block layer",
			lyr,
			l,
			log.Int("num_blocks", len(l.Blocks())))
		return errLayerHasBlock
	}

	msh.setLatestLayer(lyr)

	// layer doesn't exist, need to insert new layer
	return msh.AddZeroBlockLayer(lyr)
}

// AddTXsFromProposal adds the TXs in a Proposal into the database.
func (msh *Mesh) AddTXsFromProposal(ctx context.Context, layerID types.LayerID, proposalID types.ProposalID, txIDs []types.TransactionID) error {
	logger := msh.WithContext(ctx).WithFields(layerID, proposalID, log.Int("num_txs", len(txIDs)))
	if err := msh.conState.LinkTXsWithProposal(layerID, proposalID, txIDs); err != nil {
		logger.With().Error("failed to link proposal txs", log.Err(err))
		return err
	}
	msh.setLatestLayer(layerID)
	logger.Debug("associated txs to proposal")
	return nil
}

// AddBallot to the mesh.
func (msh *Mesh) AddBallot(ballot *types.Ballot) error {
	if err := msh.DB.AddBallot(ballot); err != nil {
		return err
	}
	msh.trtl.OnBallot(ballot)
	return nil
}

// AddBlockWithTXs adds the block and its TXs in into the database.
func (msh *Mesh) AddBlockWithTXs(ctx context.Context, block *types.Block) error {
	logger := msh.WithContext(ctx).WithFields(block.LayerIndex, block.ID(), log.Int("num_txs", len(block.TxIDs)))
	if err := msh.conState.LinkTXsWithBlock(block.LayerIndex, block.ID(), block.TxIDs); err != nil {
		logger.With().Error("failed to link block txs", log.Err(err))
		return err
	}
	msh.setLatestLayer(block.LayerIndex)
	logger.Debug("associated txs to block")

	if err := msh.AddBlock(block); err != nil {
		return err
	}
	msh.trtl.OnBlock(block)
	return nil
}

func reportRewards(block *types.Block) {
	// Report the rewards for each coinbase and each smesherID within each coinbase.
	// This can be thought of as a partition of the reward amongst all the smesherIDs
	// that added the coinbase into the block.
	for _, r := range block.Rewards {
		events.ReportRewardReceived(events.Reward{
			Layer:       block.LayerIndex,
			Total:       r.Amount,
			LayerReward: r.LayerReward,
			Coinbase:    r.Address,
			Smesher:     r.SmesherID,
		})
	}
}

// GetATXs uses GetFullAtx to return a list of atxs corresponding to atxIds requested.
func (msh *Mesh) GetATXs(ctx context.Context, atxIds []types.ATXID) (map[types.ATXID]*types.ActivationTx, []types.ATXID) {
	var mIds []types.ATXID
	atxs := make(map[types.ATXID]*types.ActivationTx, len(atxIds))
	for _, id := range atxIds {
		t, err := msh.GetFullAtx(id)
		if err != nil {
			msh.WithContext(ctx).With().Warning("could not get atx from database", id, log.Err(err))
			mIds = append(mIds, id)
		} else {
			atxs[t.ID()] = t
		}
	}
	return atxs, mIds
}
