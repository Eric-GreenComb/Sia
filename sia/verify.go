package sia

import (
	"errors"
	"math/big"
)

// Used to keep track of how many signatures an input has been signed by.
type InputSignatures struct {
	RemainingSignatures uint8
	PossibleKeys        []PublicKey
	UsedKeys            map[uint8]struct{}
}

// Add a block to the state struct.
func (s *State) AcceptBlock(b *Block) (err error) {
	bid := b.ID() // Function is not implemented.

	_, exists := s.BadBlocks[bid]
	if exists {
		err = errors.New("Block is in bad list")
		return
	}

	if b.Version != 1 {
		s.BadBlocks[bid] = struct{}{}
		err = errors.New("Block is not version 1")
		return
	}

	_, exists = s.BlockMap[bid]
	if exists {
		err = errors.New("Block exists in block map.")
		return
	}

	/*_, exists = s.OrphanBlocks[bid]
	if exists {
		err = errors.New("Block exists in orphan list")
		return
	} */

	parentBlockNode, exists := s.BlockMap[b.ParentBlock]
	if !exists {
		// OrphanBlocks[bid] = b
		err = errors.New("Block is an orphan")
		return
	}

	// If timestamp is in the future, store in future blocks list.
	// If timestamp is too far in the past, reject and put in bad blocks.

	// Check the amount of work done by the block.
	if !ValidHeader(parentBlockNode.Target, b) {
		err = errors.New("Block does not meet target")
		s.BadBlocks[bid] = struct{}{}
		return
	}

	// Add the block to the block tree.
	newBlockNode := new(BlockNode)
	newBlockNode.Block = b
	parentBlockNode.Children = append(parentBlockNode.Children, newBlockNode)
	// newBlockNode.Verified = false // implicit value, stated explicity for prosperity.
	newBlockNode.Height = parentBlockNode.Height + 1
	copy(newBlockNode.RecentTimestamps[:], parentBlockNode.RecentTimestamps[1:])
	newBlockNode.RecentTimestamps[10] = b.Timestamp
	s.BlockMap[bid] = newBlockNode

	var timePassed Timestamp
	var expectedTimePassed Timestamp
	var blockWindow BlockHeight
	if newBlockNode.Height < 5000 {
		// Calculate new target, using block 0 timestamp.
		timePassed = b.Timestamp - s.BlockRoot.Block.Timestamp
		expectedTimePassed = TargetSecondsPerBlock * Timestamp(newBlockNode.Height)
		blockWindow = newBlockNode.Height
	} else {
		// Calculate new target, using block Height-5000 timestamp.
		timePassed := b.Timestamp - s.BlockMap[s.CurrentPath[newBlockNode.Height-5000]].Block.Timestamp
		expectedTimePassed := TargetSecondsPerBlock * 5000
		blockWindow = 5000
	}

	// Adjustment as a float = timePassed / expectedTimePassed / blockWindow.
	targetAdjustment := big.NewRat(int64(timePassed), int64(expectedTimePassed)*int64(blockWindow))

	// Enforce a maximum targetAdjustment
	if targetAdjustment.Cmp(MaxAdjustmentUp) == 1 {
		targetAdjustment = MaxAdjustmentUp
	} else if targetAdjustment.Cmp(MaxAdjustmentDown) == -1 {
		targetAdjustment = MaxAdjustmentDown
	}

	// Take the target adjustment and apply it to the target slice,
	// using rational numbers. Truncate the result.
	oldTarget := new(big.Int).SetBytes(parentBlockNode.Target[:])
	ratOldTarget := new(big.Rat).SetInt(oldTarget)
	ratNewTarget := ratOldTarget.Mul(targetAdjustment, ratOldTarget)
	intNewTarget := new(big.Int).Div(ratNewTarget.Num(), ratNewTarget.Denom())
	newTargetBytes := intNewTarget.Bytes()
	offset := len(newBlockNode.Target[:]) - len(newTargetBytes)
	copy(newBlockNode.Target[offset:], newTargetBytes)

	// Add the parent target to the depth of the block in the tree.
	blockWeight := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).SetBytes(parentBlockNode.Target[:]))
	newBlockNode.Depth = BlockWeight(new(big.Rat).Add(parentBlockNode.Depth, blockWeight))

	// If the new node is .5% heavier than the other node, switch to the new fork.
	currentWeight := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).SetBytes(s.BlockMap[s.ConsensusState.CurrentBlock].Target))
	threshold := new(big.Rat).Mul(currentWeight, SurpassThreshold)
	requiredDepth = new(big.Rat).Add(s.ConsensusState.CurrentDepth, threshold)
	if newBlockNode.Depth.Cmp(requiredDepth) == 1 {
		// Find the common parent between the new fork and the current
		// fork, keeping track of which path is taken through the
		// children of the parents so that we can re-trace as we
		// validate the blocks.
		currentNode := parentBlockNode
		value := s.CurrentPath[currentNode.Height]
		var parentHistory []BlockID
		for value != currentNode.Block.ID() {
			parentHistory = append(parentHistory, currentNode.Block.ID())
			currentNode = s.BlockMap[currentNode.Block.ParentBlock]
			value = s.CurrentPath[currentNode.Height]
		}

		// Remove blocks from the ConsensusState until we get to the
		// same parent that we are forking from.
		var rewoundBlocks []BlockID
		for s.ConsensusState.CurrentBlock != currentNode.Block.ID() {
			rewoundBlocks = append(rewoundBlocks, s.ConsensusState.CurrentBlock)
			s.RewindABlock()
		}

		// Validate each block in the parent history in order, updating
		// the state as we go.  If at some point a block doesn't
		// verify, you get to walk all the way backwards and forwards
		// again.
		validatedBlocks := 0
		for i := len(parentHistory)-1; i >= 0; i-- {
			err = s.ValidateBlock(s.BlockMap[b].Block)
			if err != nil {
				// Add the whole tree of blocks to BadBlocks,
				// deleting them from BlockMap

				// Rewind the validated blocks
				for i := range validatedBlocks {
					s.RewindABlock()
				}

				// Integrate the rewound blocks
				for i := len(rewoundBlocks)-1; i >= 0; i-- {
					err = s.ValidateBlock(rewoundBlocks[i])
					if err != nil {
						panic(err)
					}
				}

				break
			}
			validatedBlocks += 1
		}

		// Do something to the transaction pool.
	} else {
		// Do something to the transaction pool.
	}

	return
}

// ValidateBlock will both verify the block AND update the consensus state.
// Calling integrate block is not needed.
func (s *State) ValidateBlock(b *Block) (err error) {
	// Check the hash on the merkle tree of transactions.

	var appliedTransactions []Transaction
	minerSubsidy := Currency(0)
	for _, txn := range b.Transactions {
		err = s.ValidateTxn(txn, s.BlockMap[b.ID()].Height)
		if err != nil {
			s.BadBlocks[b.ID()] = struct{}{}
			break
		}

		// Apply the transaction to the ConsensusState, adding it to the list of applied transactions.
		s.ApplyTransaction(txn)
		appliedTransactions = append(appliedTransactions, txn)

		minerSubsidy += txn.MinerFee
	}

	if err != nil {
		// Rewind transactions added to ConsensusState.
		for i := len(appliedTransactions) - 1; i >= 0; i-- {
			s.ReverseTransaction(appliedTransactions[i])
		}
		return
	}

	// Add outputs for all of the missed proofs in the open transactions.

	// Add coin inflation to the miner subsidy.

	// Add output contianing miner fees + block subsidy.
	bid := b.ID()
	minerSubsidyID := OutputID(HashBytes(append(bid[:], []byte("blockReward")...)))
	minerSubsidyOutput := Output{
		Value:     minerSubsidy,
		SpendHash: b.MinerAddress,
	}
	s.ConsensusState.UnspentOutputs[minerSubsidyID] = minerSubsidyOutput

	// s.BlockMap[b.ID()].Verified = true

	s.CurrentBlock = b.ID()
	s.CurrentPath[newBlockNode.Height] = b.ID()

	return
}

// Add a function that integrates a block without verifying it.

func (s *State) ValidateTxn(t Transaction, currentHeight BlockHeight) (err error) {
	if t.Version != 1 {
		err = errors.New("Transaction version is not recognized.")
		return
	}

	inputSum := Currency(0)
	outputSum := t.MinerFee
	var inputSignaturesMap map[OutputID]InputSignatures
	for _, input := range t.Inputs {
		utxo, exists := s.ConsensusState.UnspentOutputs[input.OutputID]
		if !exists {
			err = errors.New("Transaction spends a nonexisting output")
			return
		}

		inputSum += utxo.Value

		// Check that the spend conditions match the hash listed in the output.

		// Check the timelock on the spend conditions is expired.
		if input.SpendConditions.TimeLock < currentHeight {
			err = errors.New("Output spent before timelock expiry.")
			return
		}

		// Create the condition for the input signatures and add it to the input signatures map.
		_, exists = inputSignaturesMap[input.OutputID]
		if exists {
			err = errors.New("Output spent twice in same transaction")
			return
		}
		var newInputSignatures InputSignatures
		newInputSignatures.RemainingSignatures = input.SpendConditions.NumSignatures
		newInputSignatures.PossibleKeys = input.SpendConditions.PublicKeys
		inputSignaturesMap[input.OutputID] = newInputSignatures
	}

	for _, output := range t.Outputs {
		outputSum += output.Value
	}

	for _, contract := range t.FileContracts {
		if contract.Start < currentHeight {
			err = errors.New("Contract starts in the future.")
			return
		}
		if contract.End <= contract.Start {
			err = errors.New("Contract duration must be at least one block.")
			return
		}
	}

	/*
		for _, proof := range t.StorageProofs {
			// Check that the proof passes.
			// Check that the proof has not already been submitted.
		}
	*/

	if inputSum != outputSum {
		err = errors.New("Inputs do not equal outputs for transaction.")
		return
	}

	for _, sig := range t.Signatures {
		// Check that each signature signs a unique pubkey where
		// RemainingSignatures > 0.
		if inputSignaturesMap[sig.InputID].RemainingSignatures == 0 {
			err = errors.New("Friviolous Signature detected.")
			return
		}
		_, exists := inputSignaturesMap[sig.InputID].UsedKeys[sig.PublicKeyIndex]
		if exists {
			err = errors.New("public key used twice while signing")
			return
		}

		// Check the timelock on the signature.
		if sig.TimeLock < currentHeight {
			err = errors.New("signature timelock has not expired")
			return
		}

		// Check that the actual signature is valid, following the covered fields struct.
	}

	return
}

func (s *State) ApplyTransaction(t Transaction) {
	// Remove all inputs from the unspent outputs list
	for _, input := range t.Inputs {
		s.ConsensusState.SpentOutputs[input.OutputID] = s.ConsensusState.UnspentOutputs[input.OutputID]
		delete(s.ConsensusState.UnspentOutputs, input.OutputID)
	}

	// Add all outputs to the unspent outputs list
	for i, output := range t.Outputs {
		newOutputID := OutputID(HashBytes(append((t.Inputs[0].OutputID)[:], EncUint64(uint64(i))...)))
		s.ConsensusState.UnspentOutputs[newOutputID] = output
	}

	// Add all outputs created by storage proofs.
	/*
		for _, sp := range t.StorageProofs {
			// Need to check that the contract fund has sufficient funds remaining.

			newOutputID := HashBytes(append(ContractID), []byte(n))
			output := Output {
				Value: s.ConsensusState.OpenContracts[sp.ContractID].ValidProofPayout,
				SpendHash: s.ConsensusState.OpenContracts[sp.ContractID].ValidProofAddress,
			}
			s.ConsensusState.UnspentOutputs[newOutputID] = output

			// need a counter or some way to determine what the index of
			// the window is.
		}
	*/
}

// Pulls just this transaction out of the ConsensusState.
func (s *State) ReverseTransaction(t Transaction) {
	// Remove all outputs created by storage proofs.

	// Remove all outputs created by outputs.
	for i := range t.Outputs {
		outputID := OutputID(HashBytes(append((t.Inputs[0].OutputID)[:], EncUint64(uint64(i))...)))
		delete(s.ConsensusState.UnspentOutputs, outputID)
	}

	// Add all outputs spent by inputs.
	for _, input := range t.Inputs {
		s.ConsensusState.UnspentOutputs[input.OutputID] = s.ConsensusState.SpentOutputs[input.OutputID]
		delete(s.ConsensusState.SpentOutputs, input.OutputID)
	}
}

// Pulls the most recent block out of the ConsensusState.
func (s *State) RewindABlock() {
	block := s.BlockMap[s.ConsensusState.CurrentBlock].Block
	for i := len(block.Transactions) - 1; i >= 0; i-- {
		s.ReverseTransaction(block.Transactions[i])
	}

	s.ConsensusState.CurrentBlock = block.ParentBlock
}
