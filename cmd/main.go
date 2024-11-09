package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"path/filepath"

	"github.com/brevis-network/brevis-quickstart/circuits"
	"github.com/brevis-network/brevis-sdk/sdk"
	"github.com/brevis-network/brevis-sdk/sdk/proto/gwproto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var mode = flag.String("mode", "", "compile or prove")
var outDir = flag.String("out", "$HOME/circuitOut/myBrevisApp", "compilation output dir")
var srsDir = flag.String("srs", "$HOME/kzgsrs", "where to cache kzg srs")
var txHash = flag.String("tx", "", "tx hash to prove")
var rpc = flag.String("rpc", "https://eth.llamarpc.com", "eth json rpc url")
var useBrevisPartnerFlow = flag.Bool("brevis-partner", false, "use brevis partner flow")

func main() {
	flag.Parse()
	switch *mode {
	case "compile":
		compile()
	case "prove":
		prove()
	default:
		panic(fmt.Errorf("unsupported mode %s", *mode))
	}
}

func compile() {
	appCircuit := &circuits.AppCircuit{}
	// The compiled circuit, proving key, and verifying key are saved to outDir, and
	// the downloaded SRS in the process is saved to srsDir
	_, _, _, _, err := sdk.Compile(appCircuit, *outDir, *srsDir)
	check(err)
}

func prove() {
	if len(*txHash) == 0 {
		panic("-tx is required")
	}

	// Loading the previous compile result into memory
	compiledCircuit, pk, vk, _, err := sdk.ReadSetupFrom(&circuits.AppCircuit{}, *outDir)
	check(err)

	app, err := sdk.NewBrevisApp(1, *rpc, *outDir)
	check(err)

	sdkReceiptData, err := prepareReceiptData()
	check(err)

	app.AddReceipt(sdkReceiptData)

	appCircuitAssignment := &circuits.AppCircuit{}

	// Prove
	fmt.Println(">> Proving the transaction using my circuit")
	circuitInput, err := app.BuildCircuitInput(appCircuitAssignment)
	check(err)
	witness, publicWitness, err := sdk.NewFullWitness(appCircuitAssignment, circuitInput)
	check(err)
	proof, err := sdk.Prove(compiledCircuit, pk, witness)
	check(err)
	err = sdk.WriteTo(proof, filepath.Join(*outDir, "proof-"+*txHash))
	check(err)

	// Test verifying the proof we just generated
	err = sdk.Verify(vk, publicWitness, proof)
	check(err)

	fmt.Println(">> Initiating Brevis request")
	appContract := common.HexToAddress("0x9fc16c4918a4d69d885f2ea792048f13782a522d")
	refundee := common.HexToAddress("0x1bF81EA1F2F6Afde216cD3210070936401A14Bd4")

	if *useBrevisPartnerFlow {
		calldata, requestId, _, feeValue, err := app.PrepareRequest(vk, witness, 1, 11155111, refundee, appContract, 400000, gwproto.QueryOption_ZK_MODE.Enum(), "TestVolume")
		fmt.Printf("calldata %x\n", calldata)
		fmt.Printf("feeValue %d\n", feeValue)
		fmt.Printf("requestId %s\n", requestId)
		check(err)
	} else {
		calldata, requestId, _, feeValue, err := app.PrepareRequest(vk, witness, 1, 11155111, refundee, appContract, 400000, gwproto.QueryOption_ZK_MODE.Enum(), "")
		fmt.Printf("calldata %x\n", calldata)
		fmt.Printf("feeValue %d\n", feeValue)
		fmt.Printf("requestId %s\n", requestId)
		fmt.Println("Don't forget to make the transaction that pays the fee by calling Brevis.sendRequest")
		check(err)
	}

	// Submit proof to Brevis
	fmt.Println(">> Submitting my proof to Brevis")
	err = app.SubmitProof(proof)
	check(err)

	// Poll Brevis gateway for query status till the final proof is submitted
	// on-chain by Brevis and your contract is called
	fmt.Println(">> Waiting for final proof generation and submission")
	submitTx, err := app.WaitFinalProofSubmitted(context.Background())
	check(err)
	fmt.Printf(">> Final proof submitted: tx hash %s\n", submitTx)
}

func prepareReceiptData() (sdk.ReceiptData, error) {
	ec, err := ethclient.Dial(*rpc)
	if err != nil {
		return sdk.ReceiptData{}, err
	}

	hash := common.HexToHash(*txHash)
	if hash.Cmp(common.Hash{}) == 0 {
		return sdk.ReceiptData{}, fmt.Errorf("empty tx hash")
	}

	receipt, err := ec.TransactionReceipt(context.Background(), hash)
	if err != nil {
		return sdk.ReceiptData{}, err
	}

	for i, log := range receipt.Logs {
		if len(log.Data) < 32 {
			continue
		}
		if log.Address.Cmp(common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")) == 0 &&
			log.Topics[0].Cmp(common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")) == 0 &&
			big.NewInt(0).SetBytes(log.Data[0:32]).Cmp(big.NewInt(500000000)) > -1 {
			return sdk.ReceiptData{
				TxHash: hash,
				Fields: []sdk.LogFieldData{
					{
						IsTopic:    true,
						LogPos:     uint(i),
						FieldIndex: 1,
					},
					{
						IsTopic:    false,
						LogPos:     uint(i),
						FieldIndex: 0,
					},
				},
			}, nil
		}
	}

	return sdk.ReceiptData{}, fmt.Errorf("usdc transfer event not found")
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
