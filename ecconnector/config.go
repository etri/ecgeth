package ecconnector

//there are examples, if we need any configuration, we can define it here
// Defaults contains default settings for ec connector
var Defaults = Config{
	EcUrl:     "https://127.0.0.1:8780",
	SomeParam: "any",
	GasFloor:  8000000,
	GasCeil:   8000000,
}

// Config contains configuration options for Ec-geth
type Config struct {
	EcUrl     string //url of rest api of external consensus
	SomeParam string //just example. you can declare anything
	GasFloor  uint64 // Target gas floor for mined blocks.
	GasCeil   uint64 // Target gas ceiling for mined blocks.

}
