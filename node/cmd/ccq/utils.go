package ccq

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/certusone/wormhole/node/pkg/common"
	gossipv1 "github.com/certusone/wormhole/node/pkg/proto/gossip/v1"
	"github.com/certusone/wormhole/node/pkg/query"
	"github.com/wormhole-foundation/wormhole/sdk/vaa"
	"go.uber.org/zap"

	ethAbi "github.com/certusone/wormhole/node/pkg/watchers/evm/connectors/ethabi"
	ethBind "github.com/ethereum/go-ethereum/accounts/abi/bind"
	eth_common "github.com/ethereum/go-ethereum/common"
	ethClient "github.com/ethereum/go-ethereum/ethclient"
	ethRpc "github.com/ethereum/go-ethereum/rpc"
)

func FetchCurrentGuardianSet(rpcUrl, coreAddr string) (*common.GuardianSet, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	ethContract := eth_common.HexToAddress(coreAddr)
	rawClient, err := ethRpc.DialContext(ctx, rpcUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ethereum")
	}
	client := ethClient.NewClient(rawClient)
	caller, err := ethAbi.NewAbiCaller(ethContract, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create caller")
	}
	currentIndex, err := caller.GetCurrentGuardianSetIndex(&ethBind.CallOpts{Context: ctx})
	if err != nil {
		return nil, fmt.Errorf("error requesting current guardian set index: %w", err)
	}
	gs, err := caller.GetGuardianSet(&ethBind.CallOpts{Context: ctx}, currentIndex)
	if err != nil {
		return nil, fmt.Errorf("error requesting current guardian set value: %w", err)
	}
	return &common.GuardianSet{
		Keys:  gs.Keys,
		Index: currentIndex,
	}, nil
}

type Config struct {
	Permissions []User `json:"Permissions"`
}

type User struct {
	UserName     string        `json:"userName"`
	ApiKey       string        `json:"apiKey"`
	AllowedCalls []AllowedCall `json:"allowedCalls"`
}

type AllowedCall struct {
	EthCall *EthCall `json:"ethCall"`
}

type EthCall struct {
	Chain           int    `json:"chain"`
	ContractAddress string `json:"contractAddress"`
	Call            string `json:"call"`
}

type Permissions map[string]*permissionEntry

type permissionEntry struct {
	userName     string
	apiKey       string
	allowedCalls allowedCallsForUser // Key is something like "ethCall:2:000000000000000000000000b4fbf271143f4fbf7b91a5ded31805e42b2208d6:06fdde03"
}

type allowedCallsForUser map[string]struct{}

// parseConfig parses the permissions config file into a map keyed by API key.
func parseConfig(fileName string) (Permissions, error) {
	jsonFile, err := os.Open(fileName)
	if err != nil {
		return nil, fmt.Errorf(`failed to open permissions file "%s": %w`, fileName, err)
	}
	defer jsonFile.Close()

	byteValue, err := io.ReadAll(jsonFile)
	if err != nil {
		return nil, fmt.Errorf(`failed to read permissions file "%s": %w`, fileName, err)
	}

	var config Config
	if err := json.Unmarshal(byteValue, &config); err != nil {
		return nil, fmt.Errorf(`failed to unmarshal json from permissions file "%s": %w`, fileName, err)
	}

	ret := make(Permissions)
	for _, user := range config.Permissions {
		apiKey := strings.ToLower(user.ApiKey)
		if _, exists := ret[apiKey]; exists {
			return nil, fmt.Errorf(`API key "%s" in permissions file "%s" is a duplicate`, apiKey, fileName)
		}

		// Build the list of allowed calls for this API key.
		allowedCalls := make(allowedCallsForUser)
		for _, ac := range user.AllowedCalls {
			var callKey string
			if ac.EthCall != nil {
				// Convert the contract address into a standard format like "000000000000000000000000b4fbf271143f4fbf7b91a5ded31805e42b2208d6".
				contractAddress, err := vaa.StringToAddress(ac.EthCall.ContractAddress)
				if err != nil {
					return nil, fmt.Errorf(`invalid contract address "%s" for API key "%s" in permissions file "%s"`, ac.EthCall.ContractAddress, apiKey, fileName)
				}

				// The call should be the ABI four byte hex hash of the function signature. Parse it into a standard form of "06fdde03".
				call, err := hex.DecodeString(strings.TrimPrefix(ac.EthCall.Call, "0x"))
				if err != nil {
					return nil, fmt.Errorf(`invalid eth call "%s" for API key "%s" in permissions file "%s"`, ac.EthCall.Call, apiKey, fileName)
				}
				if len(call) != 4 {
					return nil, fmt.Errorf(`eth call "%s" for API key "%s" in permissions file "%s" has an invalid length, must be four bytes`, ac.EthCall.Call, apiKey, fileName)
				}

				// The permission key is the chain, contract address and call formatted as a colon separated string.
				callKey = fmt.Sprintf("ethCall:%d:%s:%s", ac.EthCall.Chain, contractAddress, hex.EncodeToString(call))
			} else {
				return nil, fmt.Errorf(`unsupported call type for API key "%s" in permissions file "%s"`, apiKey, fileName)
			}

			if _, exists := allowedCalls[callKey]; exists {
				return nil, fmt.Errorf(`"%s" is a duplicate allowed call for API key "%s" in permissions file "%s"`, callKey, apiKey, fileName)
			}

			allowedCalls[callKey] = struct{}{}
		}

		pe := &permissionEntry{
			userName:     user.UserName,
			apiKey:       apiKey,
			allowedCalls: allowedCalls,
		}

		ret[apiKey] = pe
	}

	return ret, nil
}

// validateRequest verifies that this API key is allowed to do all of the calls in this request.
func validateRequest(logger *zap.Logger, perms Permissions, apiKey string, qr *gossipv1.SignedQueryRequest) error {
	apiKey = strings.ToLower(apiKey)
	permsForUser, exists := perms[strings.ToLower(apiKey)]
	if !exists {
		return fmt.Errorf("invalid api key")
	}

	// TODO: Should we verify the signatures?

	var queryRequest query.QueryRequest
	err := queryRequest.Unmarshal(qr.QueryRequest)
	if err != nil {
		return fmt.Errorf("failed to unmarshal request: %w", err)
	}

	// Make sure the overall query request is sane.
	if err := queryRequest.Validate(); err != nil {
		return fmt.Errorf("failed to validate request: %w", err)
	}

	// Make sure they are allowed to make all of the calls that they are asking for.
	for _, pcq := range queryRequest.PerChainQueries {
		switch q := pcq.Query.(type) {
		case *query.EthCallQueryRequest:
			for _, callData := range q.CallData {
				contractAddress, err := vaa.BytesToAddress(callData.To)
				if err != nil {
					return fmt.Errorf("failed to parse contract address: %w", err)
				}
				if len(callData.Data) < 4 {
					return fmt.Errorf("eth call data must be at least four bytes")
				}
				call := hex.EncodeToString(callData.Data)
				callKey := fmt.Sprintf("ethCall:%d:%s:%s", int(pcq.ChainId), contractAddress, call)
				if _, exists := permsForUser.allowedCalls[callKey]; !exists {
					logger.Debug(`api key "%s" has requested an unauthorized call "%s"`)
					return fmt.Errorf(`call "%s" not authorized`, callKey)
				}
			}
		default:
			return fmt.Errorf("unsupported query type")
		}
	}

	return nil
}