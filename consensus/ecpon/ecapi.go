package ecpon

import "github.com/ethereum/go-ethereum/common"

type EcRpcCall interface {
	SendVerifySeal(blockno uint64, bhash common.Hash, bproof *BlockProof) error
}
