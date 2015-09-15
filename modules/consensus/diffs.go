package consensus

import (
	"errors"

	"github.com/boltdb/bolt"

	"github.com/NebulousLabs/Sia/build"
	"github.com/NebulousLabs/Sia/encoding"
	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/types"
)

var (
	errApplySiafundPoolDiffMismatch  = errors.New("committing a siafund pool diff with an invalid 'previous' field")
	errDiffsNotGenerated             = errors.New("applying diff set before generating errors")
	errInvalidSuccessor              = errors.New("generating diffs for a block that's an invalid successsor to the current block")
	errNegativePoolAdjustment        = errors.New("committing a siafund pool diff with a negative adjustment")
	errNonApplySiafundPoolDiff       = errors.New("commiting a siafund pool diff that doesn't have the 'apply' direction")
	errRevertSiafundPoolDiffMismatch = errors.New("committing a siafund pool diff with an invalid 'adjusted' field")
	errWrongAppliedDiffSet           = errors.New("applying a diff set that isn't the current block")
	errWrongRevertDiffSet            = errors.New("reverting a diff set that isn't the current block")
)

// commitDiffSetSanity performs a series of sanity checks before commiting a
// diff set.
func commitDiffSetSanity(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	// This function is purely sanity checks.
	if !build.DEBUG {
		return
	}

	// Diffs should have already been generated for this node.
	if !pb.DiffsGenerated {
		panic(errDiffsNotGenerated)
	}

	// Current node must be the input node's parent if applying, and
	// current node must be the input node if reverting.
	if dir == modules.DiffApply {
		parent := getBlockMap(tx, pb.Parent)
		if parent.Block.ID() != currentBlockID(tx) {
			panic(errWrongAppliedDiffSet)
		}
	} else {
		if pb.Block.ID() != currentBlockID(tx) {
			panic(errWrongRevertDiffSet)
		}
	}
}

// commitSiacoinOutputDiff applies or reverts a SiacoinOutputDiff.
func commitSiacoinOutputDiff(tx *bolt.Tx, scod modules.SiacoinOutputDiff, dir modules.DiffDirection) {
	if scod.Direction == dir {
		addSiacoinOutput(tx, scod.ID, scod.SiacoinOutput)
	} else {
		removeSiacoinOutput(tx, scod.ID)
	}
}

// commitFileContractDiff applies or reverts a FileContractDiff.
func commitFileContractDiff(tx *bolt.Tx, fcd modules.FileContractDiff, dir modules.DiffDirection) {
	if fcd.Direction == dir {
		addFileContract(tx, fcd.ID, fcd.FileContract)
	} else {
		removeFileContract(tx, fcd.ID)
	}
}

// commitSiafundOutputDiff applies or reverts a Siafund output diff.
func commitSiafundOutputDiff(tx *bolt.Tx, sfod modules.SiafundOutputDiff, dir modules.DiffDirection) {
	if sfod.Direction == dir {
		addSiafundOutput(tx, sfod.ID, sfod.SiafundOutput)
	} else {
		removeSiafundOutput(tx, sfod.ID)
	}
}

// commitDelayedSiacoinOutputDiff applies or reverts a delayedSiacoinOutputDiff.
func commitDelayedSiacoinOutputDiff(tx *bolt.Tx, dscod modules.DelayedSiacoinOutputDiff, dir modules.DiffDirection) {
	if dscod.Direction == dir {
		addDSCO(tx, dscod.MaturityHeight, dscod.ID, dscod.SiacoinOutput)
	} else {
		removeDSCO(tx, dscod.MaturityHeight, dscod.ID)
	}
}

// commitSiafundPoolDiff applies or reverts a SiafundPoolDiff.
func commitSiafundPoolDiff(tx *bolt.Tx, sfpd modules.SiafundPoolDiff, dir modules.DiffDirection) error {
	// Sanity check - siafund pool should only ever increase.
	if build.DEBUG {
		if sfpd.Adjusted.Cmp(sfpd.Previous) < 0 {
			panic(errNegativePoolAdjustment)
		}
		if sfpd.Direction != modules.DiffApply {
			panic(errNonApplySiafundPoolDiff)
		}
	}

	if dir == modules.DiffApply {
		// Sanity check - sfpd.Previous should equal the current siafund pool.
		if build.DEBUG && getSiafundPool(tx).Cmp(sfpd.Previous) != 0 {
			panic(errApplySiafundPoolDiffMismatch)
		}
		setSiafundPool(tx, sfpd.Adjusted)
	} else {
		// Sanity check - sfpd.Adjusted should equal the current siafund pool.
		if build.DEBUG && getSiafundPool(tx).Cmp(sfpd.Adjusted) != 0 {
			panic(errRevertSiafundPoolDiffMismatch)
		}
		setSiafundPool(tx, sfpd.Previous)
	}
	return nil
}

// createUpcomingDelayeOutputdMaps creates the delayed siacoin output maps that
// will be used when applying delayed siacoin outputs in the diff set.
func createUpcomingDelayedOutputMaps(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	if dir == modules.DiffApply {
		createDSCOBucket(tx, pb.Height+types.MaturityDelay)
	} else if pb.Height > types.MaturityDelay {
		createDSCOBucket(tx, pb.Height)
	}
}

// commitNodeDiffs commits all of the diffs in a block node.
func commitNodeDiffs(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) error {
	if dir == modules.DiffApply {
		for _, scod := range pb.SiacoinOutputDiffs {
			commitSiacoinOutputDiff(tx, scod, dir)
		}
		for _, fcd := range pb.FileContractDiffs {
			commitFileContractDiff(tx, fcd, dir)
		}
		for _, sfod := range pb.SiafundOutputDiffs {
			commitSiafundOutputDiff(tx, sfod, dir)
		}
		for _, dscod := range pb.DelayedSiacoinOutputDiffs {
			commitDelayedSiacoinOutputDiff(tx, dscod, dir)
		}
		for _, sfpd := range pb.SiafundPoolDiffs {
			err := commitSiafundPoolDiff(tx, sfpd, dir)
			if err != nil {
				return err
			}
		}
	} else {
		for i := len(pb.SiacoinOutputDiffs) - 1; i >= 0; i-- {
			commitSiacoinOutputDiff(tx, pb.SiacoinOutputDiffs[i], dir)
		}
		for i := len(pb.FileContractDiffs) - 1; i >= 0; i-- {
			commitFileContractDiff(tx, pb.FileContractDiffs[i], dir)
		}
		for i := len(pb.SiafundOutputDiffs) - 1; i >= 0; i-- {
			commitSiafundOutputDiff(tx, pb.SiafundOutputDiffs[i], dir)
		}
		for i := len(pb.DelayedSiacoinOutputDiffs) - 1; i >= 0; i-- {
			commitDelayedSiacoinOutputDiff(tx, pb.DelayedSiacoinOutputDiffs[i], dir)
		}
		for i := len(pb.SiafundPoolDiffs) - 1; i >= 0; i-- {
			err := commitSiafundPoolDiff(tx, pb.SiafundPoolDiffs[i], dir)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// deleteObsoleteDelayedOutputMaps deletes the delayed siacoin output maps that
// are no longer in use.
func deleteObsoleteDelayedOutputMaps(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	if dir == modules.DiffApply {
		// There are no outputs that mature in the first MaturityDelay blocks.
		if pb.Height > types.MaturityDelay {
			deleteDSCOBucket(tx, pb.Height)
		}
	} else {
		deleteDSCOBucket(tx, pb.Height+types.MaturityDelay)
	}
}

// updateCurrentPath updates the current path after applying a diff set.
func updateCurrentPath(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	// Update the current path.
	if dir == modules.DiffApply {
		pushPath(tx, pb.Block.ID())
	} else {
		popPath(tx)
	}
}

// commitDiffSet applies or reverts the diffs in a blockNode.
func commitDiffSet(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) error {
	// COMPATv0.4.0
	//
	// When validating/accepting a block, the types height needs to be set to
	// the height of the block that's being analyzed. After analysis is
	// finished, the height needs to be set to the height of the current block.
	//
	// This is so that the Tax() logic correctly handles the hardfork that
	// changes the tax from a float64 to a big.Rat during computation.
	types.CurrentHeightLock.Lock()
	types.CurrentHeight = pb.Height
	types.CurrentHeightLock.Unlock()

	// Sanity checks - there are a few so they were moved to another function.
	if build.DEBUG {
		commitDiffSetSanity(tx, pb, dir)
	}

	createUpcomingDelayedOutputMaps(tx, pb, dir)
	err := commitNodeDiffs(tx, pb, dir)
	if err != nil {
		return err
	}
	deleteObsoleteDelayedOutputMaps(tx, pb, dir)
	updateCurrentPath(tx, pb, dir)
	return nil
}

// generateAndApplyDiff will verify the block and then integrate it into the
// consensus state. These two actions must happen at the same time because
// transactions are allowed to depend on each other. We can't be sure that a
// transaction is valid unless we have applied all of the previous transactions
// in the block, which means we need to apply while we verify.
func generateAndApplyDiff(tx *bolt.Tx, pb *processedBlock) error {
	// COMPATv0.4.0
	//
	// When validating/accepting a block, the types height needs to be set to
	// the height of the block that's being analyzed. After analysis is
	// finished, the height needs to be set to the height of the current block.
	//
	// This is so that the Tax() logic correctly handles the hardfork that
	// changes the tax from a float64 to a big.Rat during computation.
	types.CurrentHeightLock.Lock()
	types.CurrentHeight = pb.Height
	types.CurrentHeightLock.Unlock()

	// Sanity check - the block being applied should have the current block as
	// a parent.
	if build.DEBUG && pb.Parent != currentBlockID(tx) {
		panic(errInvalidSuccessor)
	}

	bid := pb.Block.ID()
	err := tx.Bucket(BlockPath).Put(encoding.EncUint64(uint64(pb.Height)), bid[:])
	if err != nil {
		return err
	}
	createDSCOBucket(tx, pb.Height+types.MaturityDelay)

	// Validate and apply each transaction in the block. They cannot be
	// validated all at once because some transactions may not be valid until
	// previous transactions have been applied.
	for _, txn := range pb.Block.Transactions {
		err = validTransaction(tx, txn)
		if err != nil {
			return err
		}
		err = applyTransaction(tx, pb, txn)
		if err != nil {
			return err
		}
	}

	// After all of the transactions have been applied, 'maintenance' is
	// applied on the block. This includes adding any outputs that have reached
	// maturity, applying any contracts with missed storage proofs, and adding
	// the miner payouts to the list of delayed outputs.
	err = applyMaintenance(tx, pb)
	if err != nil {
		return err
	}

	updateCurrentPath(tx, pb, modules.DiffApply)

	// Sanity check preparation - set the consensus hash at this height so that
	// during reverting a check can be performed to assure consistency when
	// adding and removing blocks.
	if build.DEBUG {
		pb.ConsensusSetHash = consensusChecksum(tx)
	}

	// DiffsGenerated are only set to true after the block has been fully
	// validated and integrated. This is required to prevent later blocks from
	// being accepted on top of an invalid block - if the consensus set ever
	// forks over an invalid block, 'DiffsGenerated' will be set to 'false',
	// requiring validation to occur again. when 'DiffsGenerated' is set to
	// true, validation is skipped, therefore the flag should only be set to
	// true on fully validated blocks.
	pb.DiffsGenerated = true

	// Add the fully processed block to the block map.
	blockMap := tx.Bucket(BlockMap)
	return blockMap.Put(bid[:], encoding.Marshal(*pb))
}
