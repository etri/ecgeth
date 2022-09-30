package ecconnector

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ecpon"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

/*
type SigProof struct {
	SignedValidators []string `json:"validators"` //`json:"validators,omitempty"`
	PubKeys          [][]byte `json:"publickeys"`
	R                []byte   `json:"rValue"` // R value of multi signature
	S                []byte   `json:"SValue"` /// S value of multi signature
}
*/

//API corressponding rpc are implemented
//corressponding rpc APIs are implemented in eth/api_ecconnector.go
//let's implement dummy API
func (ec *EcConnector) HeartBeat() string {

	return "EcConnector HeartBeat"

}

func (ec *EcConnector) BuildBlock(cBlkNo uint64) (string, error) {

	log.Debug("BuildBlock called", "cBlkNo", cBlkNo)
	parent := ec.eth.BlockChain().CurrentBlock()

	/*
		if parent.NumberU64() != cBlkNo {
			return "", errors.New("you are behind in the chain")
		}
	*/
	if parent.NumberU64() > cBlkNo {
		return "", fmt.Errorf("the block(%d) is behind in the chain: %d", parent.NumberU64(), cBlkNo)
	}

	if parent.NumberU64() < cBlkNo {
		return "", fmt.Errorf("the block(%d) is the future block: %d", parent.NumberU64(), cBlkNo)
	}

	//ec.rBlkNo = cBlkNo
	block, err := ec.batcher.buildBlock(cBlkNo)

	if err != nil {
		return "", err
	}

	ec.cblock = block

	log.Info("BuildBlock is successful", "cBlkNo", cBlkNo, "bhash", block.Hash().Hex())

	return block.Hash().Hex(), nil

}

//bhash is the block hash of the candidate block, bhash is string form of hash
func (ec *EcConnector) SealBlock(blockno uint64, bhash string, proof *ecpon.BlockProof) error {

	log.Debug("SealBlock called", "blockno", blockno, "bhash", bhash)
	if ec.cblock == nil {
		return errors.New("there is no candidate block")
	}

	//rebuild hash from the string
	hash := common.HexToHash(bhash)

	if !bytes.Equal(ec.cblock.Hash().Bytes(), hash.Bytes()) {
		return errors.New("there is no block for the hash")
	}

	sblk, err := ec.batcher.SealBlock(ec.cblock, proof, nil)

	if err != nil {
		log.Debug("block sealing failed", "blockno", blockno, "error", err)
		return err
	}

	//writing the block into state
	ierr := ec.batcher.insertBlock(sblk)
	//_, ierr := ec.eth.BlockChain().InsertChain([]*types.Block{sblk})
	if ierr != nil {
		return err
	}

	log.Info("SealBlock is successful", "blockno", blockno, "bhash", bhash)

	//block is broadcasted by event, seee batcher.go insertBlock
	ec.broadcastBlock(sblk)

	return nil

}

//return last block no
func (ec *EcConnector) GetLastBlockNum() uint64 {
	blk := ec.eth.BlockChain().CurrentBlock()
	if blk != nil {
		return blk.NumberU64()
	}
	return 0

}

func (ec *EcConnector) broadcastBlock(blk *types.Block) {
	ec.eth.BroadcastBlockToAll(blk)
}
