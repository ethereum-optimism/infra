package proxyd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
)

func TestProcessTransaction(t *testing.T) {
	server := &Server{}

	testCases := []struct {
		name         string
		rawTx        string
		expectedFrom string
		expectedTo   string
		expectErr    bool
	}{
		{
			name:         "Valid ETH transaction",
			rawTx:        "0x02f9011901048405f5e10085051f4d5c00830251289499c9fc46f92e8a1c0dec1b1747d010903e884be1884563918244f40000b8a4e11013dd00000000000000000000000088fccc17adc325c0b86e0a9d3fc09cc0bf6ef2100000000000000000000000000000000000000000000000000000000000030d400000000000000000000000000000000000000000000000000000000000000060000000000000000000000000000000000000000000000000000000000000000b7375706572627269646765000000000000000000000000000000000000000000c080a068ffe0ad59ed8a7563566c2838c9f1c5d32ae6a5d1a2dd94f049ab6d587606bfa05a56aa704f28e853651c71a032c2b5f586582d73d91610f668b23d633f25c6f0",
			expectedFrom: "0x88fCcc17aDC325c0B86e0A9D3fc09Cc0bF6ef210",
			expectedTo:   "0x99C9fc46f92E8a1c0deC1b1747d010903E884bE1",
			expectErr:    false,
		},
		{
			name:         "Valid OP transaction",
			rawTx:        "0x02f8ad0a028399128f83b914d3829baa94420000000000000000000000000000000000004280b844a9059cbb000000000000000000000000420000000000000000000000000000000000004200000000000000000000000000000000000000000000003fa25ee7716cd38000c080a0cffcc8326c48c58721770bd99604cc47e6b52b11ee1d3d332558d81568701533a04b097510176885b9799ac97941caa1470990217aac2baaf20d21e04ea7b107c7",
			expectedFrom: "0xf55f12917D72087aceEC6eF749d92054bE5a071b",
			expectedTo:   "0x4200000000000000000000000000000000000042",
			expectErr:    false,
		},
		{
			name:         "Valid CELO transaction",
			rawTx:        "0x02f8b582a4ec83313b748459682f008502ad74130082cae594765de816845861e75a25fca122bb6898b8b1282a80b844a9059cbb000000000000000000000000bf5ddd312bf3f1880ec4132ff27373139028846500000000000000000000000000000000000000000000000000038d7ea4c68000c080a0ffc08f68787aec3220797e5557f6fe42604a9cf0407733aa1389c52bdce730a3a060657079bcf0d2d82b2d29ded1fbc57ed6cac144d177e1b24cf1525e9b6ac950",
			expectedFrom: "0x37B67b9f26F1901f53beF753c113AAa124200CE6",
			expectedTo:   "0x765DE816845861e75A25fCA122bb6898B8B1282a",
			expectErr:    false,
		},
		{
			name:      "Invalid transaction",
			rawTx:     "0x02f8b582a4ec83313b748459682f008502ad74130082cae594765de816845861e75a25fca122bb6898b8b1282a80b844a9059cbb000000000000000000000000bf5ddd312bf3f1880ec4132ff27373139028846500000000000000000000000000000000000000000000000000038d7ea4c68000c080a0ffc08f68787aec3220797e5557f6fe42604a9cf0407733aa1389c52bdce730a3a060657079bcf0d2d82b2d29ded1fbc57ed6cac144d177e1b24cf1525e9b6ac",
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &RPCReq{
				Params: json.RawMessage(`["` + tc.rawTx + `"]`),
			}

			tx, msg, err := server.processTransaction(context.Background(), req)

			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, tx)
				assert.NotNil(t, msg)
				assert.Equal(t, tc.expectedFrom, msg.From.Hex())
				assert.Equal(t, tc.expectedTo, tx.To().Hex())
			}
		})
	}
}
func TestFilterSanctionedAddresses(t *testing.T) {
	server := &Server{
		sanctionedAddresses: map[common.Address]struct{}{
			common.HexToAddress("0x4838B106FCe9647Bdf1E7877BF73cE8B0BAD5f97"): {},
			common.HexToAddress("0x4675c7e5baafbffbca748158becba61ef3b0a263"): {},
		},
	}

	testCases := []struct {
		name     string
		req      *RPCReq
		expected error
	}{
		{
			name: "Sender is sanctioned",
			req: &RPCReq{
				Params: json.RawMessage(`["0x02f87001830872a98084780a4d0a825208944675c7e5baafbffbca748158becba61ef3b0a263875922b6ab7cb7cd80c001a0795c9fc9d70ce247360f99b37dd4ad816a2ebb257571cb78523b4b17d03bc28fa02095ef30e1e1060f7c117cac0ca23e4b676ad6e3500beab4a3a004e20b9fe56b"]`),
			},
			expected: ErrSanctionedAddress,
		},
		{
			name: "Recipient is sanctioned",
			req: &RPCReq{
				Params: json.RawMessage(`["0x02f87001830872a98084780a4d0a825208944675c7e5baafbffbca748158becba61ef3b0a263875922b6ab7cb7cd80c001a0795c9fc9d70ce247360f99b37dd4ad816a2ebb257571cb78523b4b17d03bc28fa02095ef30e1e1060f7c117cac0ca23e4b676ad6e3500beab4a3a004e20b9fe56b"]`),
			},
			expected: ErrSanctionedAddress,
		},
		{
			name: "Neither sender nor recipient is sanctioned",
			req: &RPCReq{
				Params: json.RawMessage(`["0x02f870010c830f4240847c2b1da682520894f175e95b93a34ae6d0bf7cc978ac5219a8c747f08704d3c18e542c2a80c080a01a0cba457c7ba2f0bcee41060f55d718a4a5f321376d88949abdb853ab65fd4aa0771a3702bfa8635ec64429f5286957c6608bb87577adac1eefc79990a8498bc3"]`),
			},
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := server.filterSanctionedAddresses(context.Background(), tc.req)
			assert.Equal(t, tc.expected, err)
		})
	}
}
