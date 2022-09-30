package ecpon

import (

	//	"io"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

//extradata is inserted in block header
//nextcommittee includes information on next committee members who are reponsible for singing next block
//blkproof includes mainly signagtures of committee members who validated the block
type ExtraData struct {
	//in fact both the value must not be nil, for easy testing, nil allowed temporarilly
	NextCommittee *ConsensusGroup `rlp:"nil"`
	BlkProof      *BlockProof     `rlp:"nil"`
	// NonceUpdates  []NonceUpate consider it later
}

// ValidatorProof includes the information for convincing the validators are legitimate
// at the moment, i'll use the empty interface, later, need to define concrete structure
type ValidatorProof interface{}

type ConsensusGroup struct {
	BlockNo uint64 //Block number which this committee works for
	//Validators []enode.ID //validater list, the first one is the leader
	Validators []string // this time, string id used, later I'll use enode.ID
	/* Proof *ValidatorProof  qualification proof for validators */
}

/* old one
type BlockProof struct {
	SignedValidators []string `json:"validators"` //`json:"validators,omitempty"`
	PubKeys          [][]byte `json:"publickeys"`
	Checksum         []byte   `json:"checksum"`
	R                []byte   `json:"rValue"` // R value of multi signature
	S                []byte   `json:"sValue"` /// S value of multi signature
}
*/

type BlockProof struct {
	PubKeys  []byte `json:"publickeys"`
	Checksum []byte `json:"checksum"`
	R        []byte `json:"rValue"` // R value of multi signature
	S        []byte `json:"sValue"` /// S value of multi signature
}

func NewConsensusGroup(num uint64, validators []string) *ConsensusGroup {
	cgroup := &ConsensusGroup{
		BlockNo:    num,
		Validators: validators,
	}
	return cgroup
}

func NewBlockProof(sValidators []string, pubkeys []byte, checksum []byte, r []byte, s []byte) *BlockProof {
	bproof := &BlockProof{
		PubKeys:  pubkeys,
		Checksum: checksum,
		R:        r,
		S:        s,
	}
	return bproof
}

//read a block to get Next Committe info and the correctness proof of the block
func ReadExtra(h *types.Header) (*ConsensusGroup, *BlockProof, error) {

	var data ExtraData
	var err error

	if err = rlp.DecodeBytes(h.Extra, &data); err != nil {
		log.Trace("Failed to read extradata", "error", err)
		data.NextCommittee = nil
		data.BlkProof = nil
	}

	return data.NextCommittee, data.BlkProof, err
}

func VerifyExtraData(nextcommittee *ConsensusGroup, blkproof *BlockProof) error {
	//implement verifying code
	return nil
}
