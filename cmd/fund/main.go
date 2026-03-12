// Quick utility to transfer HBAR between testnet accounts.
// Usage: go run ./cmd/fund
package main

import (
	"fmt"
	"os"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"
)

func main() {
	senderAcct := os.Getenv("INFERENCE_HEDERA_ACCOUNT_ID")
	senderKeyStr := os.Getenv("INFERENCE_HEDERA_PRIVATE_KEY")
	receiverAcct := os.Getenv("HEDERA_COORDINATOR_ACCOUNT_ID")

	if senderAcct == "" || senderKeyStr == "" || receiverAcct == "" {
		fmt.Fprintln(os.Stderr, "set INFERENCE_HEDERA_ACCOUNT_ID, INFERENCE_HEDERA_PRIVATE_KEY, HEDERA_COORDINATOR_ACCOUNT_ID")
		os.Exit(1)
	}

	senderID, _ := hiero.AccountIDFromString(senderAcct)
	senderKey, _ := hiero.PrivateKeyFromStringECDSA(senderKeyStr)
	receiverID, _ := hiero.AccountIDFromString(receiverAcct)

	client := hiero.ClientForTestnet()
	client.SetOperator(senderID, senderKey)

	amount := hiero.NewHbar(50)

	tx, err := hiero.NewTransferTransaction().
		AddHbarTransfer(senderID, amount.Negated()).
		AddHbarTransfer(receiverID, amount).
		Execute(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "transfer failed: %v\n", err)
		os.Exit(1)
	}

	receipt, err := tx.GetReceipt(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "receipt failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Transfer successful! Status: %s\n", receipt.Status)
	fmt.Printf("Sent %s from %s to %s\n", amount, senderID, receiverID)
	fmt.Printf("Transaction ID: %s\n", tx.TransactionID)
}
