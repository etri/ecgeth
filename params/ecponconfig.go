package params

type ExternalConsensusConfig struct {
	Authorizer        string `json:"authorizer"`
	ConsensusProtocol string `json:"cs_protocol"`
	ConsensusTimeout  uint32 `json:"cs_timeout"`
	BlockInterval     uint32 `json:"block_interval"`
}

func (c *ExternalConsensusConfig) String() string {
	return "ExternalConsensusConfig"
}
