// implements consensus interface.
package ecpon

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie"
	"golang.org/x/crypto/sha3"
)

// pon+EcPonBada protocol constants.
var (
	notUseDifficulty = big.NewInt(1)
	uncleHash        = types.CalcUncleHash(nil) // Always Keccak256(RLP([])) as uncles are meaningless outside of PoW.
)

//These error are copied from clique
//many of them maybe is not useful.
//I need to prune them

// Various error messages to mark blocks invalid. These should be private to
// prevent engine specific errors from being referenced in the remainder of the
// codebase, inherently breaking if the engine is swapped out. Please put common
// error types into the consensus package.
var (
	// errUnknownBlock is returned when the list of signers is requested for a block
	// that is not part of the local blockchain.
	errUnknownBlock = errors.New("unknown block")

	// errInvalidCheckpointBeneficiary is returned if a checkpoint/epoch transition
	// block has a beneficiary set to non-zeroes.
	errInvalidCheckpointBeneficiary = errors.New("beneficiary in checkpoint block non-zero")

	// errInvalidVote is returned if a nonce value is something else that the two
	// allowed constants of 0x00..0 or 0xff..f.
	errInvalidVote = errors.New("vote nonce not 0x00..0 or 0xff..f")

	// errInvalidCheckpointVote is returned if a checkpoint/epoch transition block
	// has a vote nonce set to non-zeroes.
	errInvalidCheckpointVote = errors.New("vote nonce in checkpoint block non-zero")

	// errMissingVanity is returned if a block's extra-data section is shorter than
	// 32 bytes, which is required to store the signer vanity.
	errMissingVanity = errors.New("extra-data 32 byte vanity prefix missing")

	// errMissingSignature is returned if a block's extra-data section doesn't seem
	// to contain a 65 byte secp256k1 signature.
	errMissingSignature = errors.New("extra-data 65 byte signature suffix missing")

	// errExtraSigners is returned if non-checkpoint block contain signer data in
	// their extra-data fields.
	errExtraSigners = errors.New("non-checkpoint block contains extra signer list")

	// errInvalidCheckpointSigners is returned if a checkpoint block contains an
	// invalid list of signers (i.e. non divisible by 20 bytes).
	errInvalidCheckpointSigners = errors.New("invalid signer list on checkpoint block")

	// errMismatchingCheckpointSigners is returned if a checkpoint block contains a
	// list of signers different than the one the local node calculated.
	errMismatchingCheckpointSigners = errors.New("mismatching signer list on checkpoint block")

	// errInvalidMixDigest is returned if a block's mix digest is non-zero.
	errInvalidMixDigest = errors.New("non-zero mix digest")

	// errInvalidUncleHash is returned if a block contains an non-empty uncle list.
	errInvalidUncleHash = errors.New("non empty uncle hash")

	// errInvalidDifficulty is returned if the difficulty of a block neither 1 or 2.
	errInvalidDifficulty = errors.New("invalid difficulty")

	// errWrongDifficulty is returned if the difficulty of a block doesn't match the
	// turn of the signer.
	errWrongDifficulty = errors.New("wrong difficulty")

	// errInvalidTimestamp is returned if the timestamp of a block is lower than
	// the previous block's timestamp + the minimum block period.
	errInvalidTimestamp = errors.New("invalid timestamp")

	// errInvalidVotingChain is returned if an authorization list is attempted to
	// be modified via out-of-range or non-contiguous headers.
	errInvalidVotingChain = errors.New("invalid voting chain")

	// errUnauthorizedSigner is returned if a header is signed by a non-authorized entity.
	errUnauthorizedSigner = errors.New("unauthorized signer")

	// errRecentlySigned is returned if a header is signed by an authorized entity
	// that already signed a header recently, thus is temporarily not allowed to.
	errRecentlySigned = errors.New("recently signed")
)

type EcPonBada struct {
	config    *params.ExternalConsensusConfig // Consensus engine configuration parameters
	db        ethdb.Database                  // Database to store and retrieve snapshot checkpoints
	ecRpcCall EcRpcCall

	//...
}

// New creates a EcPonBada consensus engine
func New(config *params.ExternalConsensusConfig, db ethdb.Database) *EcPonBada {

	return &EcPonBada{
		config: config,
		db:     db,
	}
}

//this is the api only supported in EcPonBada consensus api
//register ecrpchandler
func (c *EcPonBada) SetEcRpcHandler(handler EcRpcCall) {
	c.ecRpcCall = handler
}

// Author implements consensus.Engine, returning the Ethereum address recovered
func (c *EcPonBada) Author(header *types.Header) (common.Address, error) {

	//implement the code, Author will returnd address of the leader
	var signer common.Address
	return signer, nil
}

// VerifyHeader checks whether a header conforms to the consensus rules.
func (c *EcPonBada) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header, seal bool) error {
	return c.verifyHeader(chain, header, nil)
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers. The
// method returns a quit channel to abort the operations and a results channel to
// retrieve the async verifications (the order is that of the input slice).
func (c *EcPonBada) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	abort := make(chan struct{})
	results := make(chan error, len(headers))

	go func() {
		for i, header := range headers {
			err := c.verifyHeader(chain, header, headers[:i])

			select {
			case <-abort:
				return
			case results <- err:
			}
		}
	}()
	return abort, results
}

// verifyHeader checks whether a header conforms to the consensus rules.The
// caller may optionally pass in a batch of parents (ascending order) to avoid
// looking those up from the database. This is useful for concurrently verifying
// a batch of new headers.

//need to reimplement it in order to take care of pon/bada
func (c *EcPonBada) verifyHeader(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	if header.Number == nil {
		return errUnknownBlock
	}

	// Don't waste time checking blocks from the future
	// this is too tight, sometimes, even block is fine in terms of time
	// the error is activated,
	//let's allow small margine. (2 second), margine must be >=2
	//if header.Time > uint64(time.Now().Unix()) {

	log.Trace("Timestamp comparision", "header", header.Time, "now", (time.Now().Unix()))
	if header.Time > uint64(time.Now().Unix()+2) {

		return consensus.ErrFutureBlock
	}

	// Ensure that the block doesn't contain any uncles which are meaningless in PoA
	if header.UncleHash != uncleHash {
		return errInvalidUncleHash
	}

	// If all checks passed, validate any special fields for hard forks
	if err := misc.VerifyForkHashes(chain.Config(), header, false); err != nil {
		return err
	}
	// All basic checks passed, verify cascading fields
	return c.verifyCascadingFields(chain, header, parents)
}

// verifyCascadingFields verifies all the header fields that are not standalone,
// rather depend on a batch of previous headers. The caller may optionally pass
// in a batch of parents (ascending order) to avoid looking those up from the
// database. This is useful for concurrently verifying a batch of new headers.
func (c *EcPonBada) verifyCascadingFields(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	// The genesis block is the always valid dead-end
	number := header.Number.Uint64()
	if number == 0 {
		return nil
	}

	//need to implement

	// All basic checks passed, verify the seal and return
	return c.verifySeal(chain, header, parents)
}

// VerifyUncles implements consensus.Engine, always returning an error for any
// uncles as this consensus mechanism doesn't permit uncles.
func (c *EcPonBada) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	if len(block.Uncles()) > 0 {
		return errors.New("uncles not allowed")
	}
	return nil
}

// verifySeal checks whether the signature contained in the header satisfies the
// consensus protocol requirements. The method accepts an optional list of parent
// headers that aren't yet part of the local blockchain to generate the snapshots
// from.
func (c *EcPonBada) verifySeal(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	// Verifying the genesis block is not supported
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}

	_, bproof, err := ReadExtra(header)

	if err != nil {
		return err
	}

	//
	log.Debug("verifySeal called. try to call remote api", "blockno", number)

	if c.ecRpcCall == nil {
		return errors.New("remote API is not ready yet")
	}
	return c.ecRpcCall.SendVerifySeal(number, header.Hash(), bproof)
}

// Prepare implements consensus.Engine, preparing all the consensus fields of the
// header for running the transactions on top.
func (c *EcPonBada) Prepare(chain consensus.ChainHeaderReader, header *types.Header) error {

	log.Debug("Prepare Called: Preparing header of ec-geth block")
	header.Difficulty = notUseDifficulty
	header.Nonce = types.BlockNonce{}
	header.Coinbase = common.Address{}

	//need to implement further

	// populating Extradata
	//this time, empty.
	//header.Extra = ..

	// Mix digest is reserved for now, set to empty
	header.MixDigest = common.Hash{}

	return nil

}

// Finalize implements consensus.Engine, ensuring no uncles are set, nor block
// rewards given.
func (c *EcPonBada) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header) {

	log.Debug("Finalize Called: Preparing header of ec-geth block")
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
	header.UncleHash = types.CalcUncleHash(nil)
}

// FinalizeAndAssemble implements consensus.Engine, ensuring no uncles are set,
// nor block rewards given, and returns the final block.
func (c *EcPonBada) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error) {
	// Finalize block
	c.Finalize(chain, header, state, txs, uncles)

	// Assemble and return the final block for sealing
	return types.NewBlock(header, txs, nil, receipts, trie.NewStackTrie(nil)), nil
}

// Seal implements consensus.Engine, attempting to create a sealed block using
// multi signature
//it is not used, instead, below API is used
func (c *EcPonBada) Seal(chain consensus.ChainHeaderReader, block *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {

	//need to implement
	return nil
}

// Seal implements consensus.Engine, attempting to create a sealed block using
// multi signature
//this API is not defined in Consensus Interface
//EcPonBada, we can seal immediately, we don't need channel
//we put signature data and next committee information in the extradata field of header
func (c *EcPonBada) SealEcPonBada(chain consensus.ChainHeaderReader, block *types.Block, blockProof *BlockProof, nextcommittee *ConsensusGroup) (*types.Block, error) {

	var extra ExtraData

	extra.BlkProof = blockProof
	extra.NextCommittee = nextcommittee

	msg := block.Header().HashForEcPon().Bytes()
	log.Debug(fmt.Sprintf("Sealing the block: data %x, signatureR %x, signatureS %x", msg, blockProof.R, blockProof.S))

	bytes, err := rlp.EncodeToBytes(extra)

	if err == nil {
		log.Trace("injection of extra data successful")

		// you must be very carful with block.Header()
		// it always returns a copy of header
		/// you can not directly access the header of block using block.Header()

		header := block.Header()
		header.Extra = bytes

		return block.WithSeal(header), nil
	} else {
		log.Debug("Failed to inject extra data")
		return nil, errors.New("Failed to inject extra data")
	}

}

//let's just return notUsedDifficulty
//later, we need to implement it.
func (c *EcPonBada) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *types.Header) *big.Int {

	return notUseDifficulty
}

// SealHash returns the hash of a block prior to it being sealed.
func (c *EcPonBada) SealHash(header *types.Header) common.Hash {
	return SealHash(header)
}

// Close implements consensus.Engine. It's a noop for clique as there are no background threads.
func (c *EcPonBada) Close() error {
	return nil
}

// APIs implements consensus.Engine, returning the user facing RPC API to allow
// controlling the signer voting.
func (c *EcPonBada) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	return []rpc.API{{
		Namespace: "ecponbada",
		Version:   "1.0",
		Service:   &API{chain: chain, ecponbada: c},
		Public:    false,
	}}
}

// SealHash returns the hash of a block prior to it being sealed.
//need to implement
func SealHash(header *types.Header) (hash common.Hash) {
	hasher := sha3.NewLegacyKeccak256()

	rlp.Encode(hasher, []interface{}{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		header.Bloom,
		header.Difficulty,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		header.Extra,
	})
	hasher.Sum(hash[:0])
	return hash
}
