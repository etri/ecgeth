package eth

import (
	"github.com/ethereum/go-ethereum/consensus/ecpon"
)

// PublicEcConnectorAPI provides an API to access ecconnector in order to interconnect with
// External consensu Engine
// this time, it is implemented as Public, later I'll consider to change it as Private
type PublicEcConnectorAPI struct {
	e *Ethereum
}

// NewPublicEthereumAPI creates a new Ethereum protocol API for full nodes.
func NewPublicEcConnectorAPI(e *Ethereum) *PublicEcConnectorAPI {
	return &PublicEcConnectorAPI{e}
}

//let's implement dummy API
func (api *PublicEcConnectorAPI) HeartBeat() string {

	return api.e.EcConnector().HeartBeat()
}

func (api *PublicEcConnectorAPI) BuildBlock(cBlkNo uint64) (string, error) {

	return api.e.EcConnector().BuildBlock(cBlkNo)

}

func (api *PublicEcConnectorAPI) SealBlock(blockno uint64, bhash string, proof *ecpon.BlockProof) error {

	return api.e.EcConnector().SealBlock(blockno, bhash, proof)

}

func (api *PublicEcConnectorAPI) GetLastBlockNum() uint64 {
	return api.e.EcConnector().GetLastBlockNum()
}
