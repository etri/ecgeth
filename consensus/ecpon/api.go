// API exposes PON/BADA related methods for the RPC interfaces

package ecpon

import "github.com/ethereum/go-ethereum/consensus"

// API is a user facing RPC API to allow controlling PON/BADA consensus
type API struct {
	chain     consensus.ChainHeaderReader
	ecponbada *EcPonBada
}

//let's implement dummy API
func (api *API) Name() string {

	return "ecpon"

}
