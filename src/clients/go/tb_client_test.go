package tigerbeetle_go

import (
	"bytes"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"unsafe"

	"github.com/tigerbeetle/tigerbeetle-go/assert"
	"github.com/tigerbeetle/tigerbeetle-go/pkg/types"
)

const (
	TIGERBEETLE_PORT                   = "3000"
	TIGERBEETLE_CLUSTER_ID      uint64 = 0
	TIGERBEETLE_REPLICA_ID      uint32 = 0
	TIGERBEETLE_REPLICA_COUNT   uint32 = 1
	TIGERBEETLE_CONCURRENCY_MAX uint   = 8192
)

func HexStringToUint128(value string) types.Uint128 {
	number, err := types.HexStringToUint128(value)
	if err != nil {
		panic(err)
	}
	return number
}

func WithClient(s testing.TB, withClient func(Client)) {
	var tigerbeetlePath string
	if runtime.GOOS == "windows" {
		tigerbeetlePath = "../../../tigerbeetle.exe"
	} else {
		tigerbeetlePath = "../../../tigerbeetle"
	}

	addressArg := "--addresses=" + TIGERBEETLE_PORT
	cacheSizeArg := "--cache-grid=512MiB"
	replicaArg := fmt.Sprintf("--replica=%d", TIGERBEETLE_REPLICA_ID)
	replicaCountArg := fmt.Sprintf("--replica-count=%d", TIGERBEETLE_REPLICA_COUNT)
	clusterArg := fmt.Sprintf("--cluster=%d", TIGERBEETLE_CLUSTER_ID)

	fileName := fmt.Sprintf("./%d_%d.tigerbeetle", TIGERBEETLE_CLUSTER_ID, TIGERBEETLE_REPLICA_ID)
	_ = os.Remove(fileName)

	tbInit := exec.Command(tigerbeetlePath, "format", clusterArg, replicaArg, replicaCountArg, fileName)
	var tbErr bytes.Buffer
	tbInit.Stdout = &tbErr
	tbInit.Stderr = &tbErr
	if err := tbInit.Run(); err != nil {
		fmt.Println(fmt.Sprint(err) + ": " + tbErr.String())
		s.Fatal(err)
	}

	s.Cleanup(func() {
		_ = os.Remove(fileName)
	})

	tbStart := exec.Command(tigerbeetlePath, "start", addressArg, cacheSizeArg, fileName)
	if err := tbStart.Start(); err != nil {
		s.Fatal(err)
	}

	s.Cleanup(func() {
		if err := tbStart.Process.Kill(); err != nil {
			s.Fatal(err)
		}
	})

	addresses := []string{"127.0.0.1:" + TIGERBEETLE_PORT}
	client, err := NewClient(types.ToUint128(TIGERBEETLE_CLUSTER_ID), addresses, TIGERBEETLE_CONCURRENCY_MAX)
	if err != nil {
		s.Fatal(err)
	}

	s.Cleanup(func() {
		client.Close()
	})

	withClient(client)
}

func TestClient(s *testing.T) {
	WithClient(s, func(client Client) {
		doTestClient(s, client)
	})
}

func doTestClient(s *testing.T, client Client) {
	accountA := types.Account{
		ID:     HexStringToUint128("a"),
		Ledger: 1,
		Code:   1,
	}
	accountB := types.Account{
		ID:     HexStringToUint128("b"),
		Ledger: 1,
		Code:   2,
	}

	s.Run("can create accounts", func(t *testing.T) {
		results, err := client.CreateAccounts([]types.Account{
			accountA,
			accountB,
		})
		if err != nil {
			t.Fatal(err)
		}

		assert.Empty(t, results)
	})

	s.Run("can lookup accounts", func(t *testing.T) {
		results, err := client.LookupAccounts([]types.Uint128{
			accountA.ID,
			accountB.ID,
		})
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, results, 2)
		accA := results[0]
		assert.Equal(t, uint32(1), accA.Ledger)
		assert.Equal(t, uint16(1), accA.Code)
		assert.Equal(t, uint16(0), accA.Flags)
		assert.Equal(t, types.ToUint128(0), accA.DebitsPending)
		assert.Equal(t, types.ToUint128(0), accA.DebitsPosted)
		assert.Equal(t, types.ToUint128(0), accA.CreditsPending)
		assert.Equal(t, types.ToUint128(0), accA.CreditsPosted)
		assert.NotEqual(t, uint64(0), accA.Timestamp)
		assert.Equal(t, unsafe.Sizeof(accA), 128)

		accB := results[1]
		assert.Equal(t, uint32(1), accB.Ledger)
		assert.Equal(t, uint16(2), accB.Code)
		assert.Equal(t, uint16(0), accB.Flags)
		assert.Equal(t, types.ToUint128(0), accB.DebitsPending)
		assert.Equal(t, types.ToUint128(0), accB.DebitsPosted)
		assert.Equal(t, types.ToUint128(0), accB.CreditsPending)
		assert.Equal(t, types.ToUint128(0), accB.CreditsPosted)
		assert.NotEqual(t, uint64(0), accB.Timestamp)
	})

	s.Run("can create a transfer", func(t *testing.T) {
		results, err := client.CreateTransfers([]types.Transfer{
			{
				ID:              HexStringToUint128("a"),
				CreditAccountID: accountA.ID,
				DebitAccountID:  accountB.ID,
				Amount:          types.ToUint128(100),
				Ledger:          1,
				Code:            1,
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		assert.Empty(t, results)

		accounts, err := client.LookupAccounts([]types.Uint128{accountA.ID, accountB.ID})
		if err != nil {
			t.Fatal(err)
		}
		assert.Len(t, accounts, 2)

		accountA = accounts[0]
		assert.Equal(t, types.ToUint128(0), accountA.DebitsPending)
		assert.Equal(t, types.ToUint128(0), accountA.DebitsPosted)
		assert.Equal(t, types.ToUint128(0), accountA.CreditsPending)
		assert.Equal(t, types.ToUint128(100), accountA.CreditsPosted)

		accountB = accounts[1]
		assert.Equal(t, types.ToUint128(0), accountB.DebitsPending)
		assert.Equal(t, types.ToUint128(100), accountB.DebitsPosted)
		assert.Equal(t, types.ToUint128(0), accountB.CreditsPending)
		assert.Equal(t, types.ToUint128(0), accountB.CreditsPosted)
	})

	s.Run("can create linked transfers", func(t *testing.T) {
		transfer1 := types.Transfer{
			ID:              HexStringToUint128("d"),
			CreditAccountID: accountA.ID,
			DebitAccountID:  accountB.ID,
			Amount:          types.ToUint128(50),
			Flags:           types.TransferFlags{Linked: true}.ToUint16(), // points to transfer 2
			Code:            1,
			Ledger:          1,
		}
		transfer2 := types.Transfer{
			ID:              HexStringToUint128("d"),
			CreditAccountID: accountA.ID,
			DebitAccountID:  accountB.ID,
			Amount:          types.ToUint128(50),
			// Does not have linked flag as it is the end of the chain.
			// This will also cause it to fail as this is now a duplicate with different flags
			Flags:  0,
			Code:   1,
			Ledger: 1,
		}
		results, err := client.CreateTransfers([]types.Transfer{transfer1, transfer2})
		if err != nil {
			t.Fatal(err)
		}
		assert.Len(t, results, 2)
		assert.Equal(t, unsafe.Sizeof(transfer1), 128)
		assert.Equal(t, types.TransferEventResult{Index: 0, Result: types.TransferLinkedEventFailed}, results[0])
		assert.Equal(t, types.TransferEventResult{Index: 1, Result: types.TransferExistsWithDifferentFlags}, results[1])

		accounts, err := client.LookupAccounts([]types.Uint128{accountA.ID, accountB.ID})
		if err != nil {
			t.Fatal(err)
		}
		assert.Len(t, accounts, 2)

		accountA = accounts[0]
		assert.Equal(t, types.ToUint128(100), accountA.CreditsPosted)
		assert.Equal(t, types.ToUint128(0), accountA.CreditsPending)
		assert.Equal(t, types.ToUint128(0), accountA.DebitsPosted)
		assert.Equal(t, types.ToUint128(0), accountA.DebitsPending)

		accountB = accounts[1]
		assert.Equal(t, types.ToUint128(0), accountB.CreditsPosted)
		assert.Equal(t, types.ToUint128(0), accountB.CreditsPending)
		assert.Equal(t, types.ToUint128(100), accountB.DebitsPosted)
		assert.Equal(t, types.ToUint128(0), accountB.DebitsPending)
	})

	s.Run("can create concurrent transfers", func(t *testing.T) {
		const TRANSFERS_MAX = 1_000_000
		concurrencyMax := make(chan struct{}, TIGERBEETLE_CONCURRENCY_MAX)

		accounts, err := client.LookupAccounts([]types.Uint128{accountA.ID, accountB.ID})
		if err != nil {
			t.Fatal(err)
		}
		assert.Len(t, accounts, 2)
		accountACredits := accounts[0].CreditsPosted.BigInt()
		accountBDebits := accounts[1].DebitsPosted.BigInt()

		var waitGroup sync.WaitGroup
		for i := 0; i < TRANSFERS_MAX; i++ {
			waitGroup.Add(1)

			go func(i int) {
				defer waitGroup.Done()

				concurrencyMax <- struct{}{}
				results, err := client.CreateTransfers([]types.Transfer{
					{
						ID:              types.ToUint128(uint64(TRANSFERS_MAX + i)),
						CreditAccountID: accountA.ID,
						DebitAccountID:  accountB.ID,
						Amount:          types.ToUint128(1),
						Ledger:          1,
						Code:            1,
					},
				})
				<-concurrencyMax
				if err != nil {
					t.Fatal(err)
				}

				assert.Empty(t, results)
			}(i)
		}
		waitGroup.Wait()

		accounts, err = client.LookupAccounts([]types.Uint128{accountA.ID, accountB.ID})
		if err != nil {
			t.Fatal(err)
		}
		assert.Len(t, accounts, 2)
		accountACreditsAfter := accounts[0].CreditsPosted.BigInt()
		accountBDebitsAfter := accounts[1].DebitsPosted.BigInt()

		// Each transfer moves ONE unit,
		// so the credit/debit must differ from TRANSFERS_MAX units:
		assert.Equal(t, TRANSFERS_MAX, big.NewInt(0).Sub(&accountACreditsAfter, &accountACredits).Int64())
		assert.Equal(t, TRANSFERS_MAX, big.NewInt(0).Sub(&accountBDebitsAfter, &accountBDebits).Int64())
	})

	s.Run("can query transfers for an account", func(t *testing.T) {
		// Create a new account:
		accountC := types.Account{
			ID:     HexStringToUint128("c"),
			Ledger: 1,
			Code:   1,
			Flags: types.AccountFlags{
				History: true,
			}.ToUint16(),
		}
		account_results, err := client.CreateAccounts([]types.Account{accountC})
		if err != nil {
			t.Fatal(err)
		}
		assert.Len(t, account_results, 0)

		// Create transfers where the new account is either the debit or credit account:
		transfers_created := make([]types.Transfer, 10)
		for i := 0; i < 10; i++ {
			transfer_id := types.ToUint128(uint64(i) + 10_000)

			// Swap debit and credit accounts:
			if i%2 == 0 {
				transfers_created[i] = types.Transfer{
					ID:              transfer_id,
					CreditAccountID: accountA.ID,
					DebitAccountID:  accountC.ID,
					Amount:          types.ToUint128(50),
					Flags:           0,
					Code:            1,
					Ledger:          1,
				}
			} else {
				transfers_created[i] = types.Transfer{
					ID:              transfer_id,
					CreditAccountID: accountC.ID,
					DebitAccountID:  accountB.ID,
					Amount:          types.ToUint128(50),
					Flags:           0,
					Code:            1,
					Ledger:          1,
				}
			}
		}
		transfer_results, err := client.CreateTransfers(transfers_created)
		if err != nil {
			t.Fatal(err)
		}
		assert.Len(t, transfer_results, 0)

		// Query all transfers for accountC:
		filter := types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: 0,
			Limit:        8190,
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: false,
			}.ToUint32(),
		}
		transfers_retrieved, err := client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err := client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, len(transfers_created))
		assert.Len(t, account_history, len(transfers_retrieved))

		timestamp := uint64(0)
		for i, transfer := range transfers_retrieved {
			assert.True(t, timestamp < transfer.Timestamp)
			timestamp = transfer.Timestamp

			assert.True(t, transfer.Timestamp == account_history[i].Timestamp)
		}

		// Query only the debit transfers for accountC, descending:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: 0,
			Limit:        8190,
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  false,
				Reversed: true,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, len(transfers_created)/2)
		assert.Len(t, account_history, len(transfers_retrieved))

		timestamp = ^uint64(0)
		for i, transfer := range transfers_retrieved {
			assert.True(t, transfer.Timestamp < timestamp)
			timestamp = transfer.Timestamp

			assert.True(t, transfer.Timestamp == account_history[i].Timestamp)
		}

		// Query only the credit transfers for accountC, descending:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: 0,
			Limit:        8190,
			Flags: types.AccountFilterFlags{
				Debits:   false,
				Credits:  true,
				Reversed: true,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, len(transfers_created)/2)
		assert.Len(t, account_history, len(transfers_retrieved))

		timestamp = ^uint64(0)
		for i, transfer := range transfers_retrieved {
			assert.True(t, transfer.Timestamp < timestamp)
			timestamp = transfer.Timestamp

			assert.True(t, transfer.Timestamp == account_history[i].Timestamp)
		}

		// Query the first 5 transfers for accountC:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: 0,
			Limit:        uint32(len(transfers_created) / 2),
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: false,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, len(transfers_created)/2)
		assert.Len(t, account_history, len(transfers_retrieved))

		timestamp = 0
		for i, transfer := range transfers_retrieved {
			assert.True(t, timestamp < transfer.Timestamp)
			timestamp = transfer.Timestamp

			assert.True(t, transfer.Timestamp == account_history[i].Timestamp)
		}

		// Query the next 5 transfers for accountC, with pagination:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: timestamp + 1,
			TimestampMax: 0,
			Limit:        uint32(len(transfers_created) / 2),
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: false,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, len(transfers_created)/2)
		assert.Len(t, account_history, len(transfers_retrieved))

		for i, transfer := range transfers_retrieved {
			assert.True(t, timestamp < transfer.Timestamp)
			timestamp = transfer.Timestamp

			assert.True(t, transfer.Timestamp == account_history[i].Timestamp)
		}

		// Query again, no more transfers should be found:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: timestamp + 1,
			TimestampMax: 0,
			Limit:        uint32(len(transfers_created) / 2),
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: false,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, 0)
		assert.Len(t, account_history, len(transfers_retrieved))

		// Query the first 5 transfers for accountC order by DESC:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: 0,
			Limit:        uint32(len(transfers_created) / 2),
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: true,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, len(transfers_created)/2)
		assert.Len(t, account_history, len(transfers_retrieved))

		timestamp = ^uint64(0)
		for i, transfer := range transfers_retrieved {
			assert.True(t, timestamp > transfer.Timestamp)
			timestamp = transfer.Timestamp

			assert.True(t, transfer.Timestamp == account_history[i].Timestamp)
		}

		// Query the next 5 transfers for accountC, with pagination:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: timestamp - 1,
			Limit:        uint32(len(transfers_created) / 2),
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: true,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, len(transfers_created)/2)
		assert.Len(t, account_history, len(transfers_retrieved))

		for i, transfer := range transfers_retrieved {
			assert.True(t, timestamp > transfer.Timestamp)
			timestamp = transfer.Timestamp

			assert.True(t, transfer.Timestamp == account_history[i].Timestamp)
		}

		// Query again, no more transfers should be found:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: timestamp - 1,
			Limit:        uint32(len(transfers_created) / 2),
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: true,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, 0)
		assert.Len(t, account_history, len(transfers_retrieved))

		// Invalid account:
		filter = types.AccountFilter{
			AccountID:    types.ToUint128(0),
			TimestampMin: 0,
			TimestampMax: 0,
			Limit:        8190,
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: false,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, 0)
		assert.Len(t, account_history, len(transfers_retrieved))

		// Invalid timestamp min:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: ^uint64(0), // ulong max value
			TimestampMax: 0,
			Limit:        8190,
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: false,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, 0)
		assert.Len(t, account_history, len(transfers_retrieved))

		// Invalid timestamp max:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: ^uint64(0), // ulong max value
			Limit:        8190,
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: false,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, 0)
		assert.Len(t, account_history, len(transfers_retrieved))

		// Invalid timestamps:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: (^uint64(0)) - 1, // ulong max - 1
			TimestampMax: 1,
			Limit:        8190,
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: false,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, 0)
		assert.Len(t, account_history, len(transfers_retrieved))

		// Zero limit:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: 0,
			Limit:        0,
			Flags: types.AccountFilterFlags{
				Debits:   true,
				Credits:  true,
				Reversed: false,
			}.ToUint32(),
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, 0)
		assert.Len(t, account_history, len(transfers_retrieved))

		// Empty flags:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: 0,
			Limit:        8190,
			Flags:        0,
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, 0)
		assert.Len(t, account_history, len(transfers_retrieved))

		// Invalid flags:
		filter = types.AccountFilter{
			AccountID:    accountC.ID,
			TimestampMin: 0,
			TimestampMax: 0,
			Limit:        8190,
			Flags:        0xFFFF,
		}
		transfers_retrieved, err = client.GetAccountTransfers(filter)
		if err != nil {
			t.Fatal(err)
		}
		account_history, err = client.GetAccountHistory(filter)
		if err != nil {
			t.Fatal(err)
		}

		assert.Len(t, transfers_retrieved, 0)
		assert.Len(t, account_history, len(transfers_retrieved))
	})

}

func BenchmarkNop(b *testing.B) {
	WithClient(b, func(client Client) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := client.Nop(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
