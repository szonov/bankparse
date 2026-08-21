package payment

import "testing"

func TestDocumentAmountHasExplicitCurrency(t *testing.T) {
	d := Document{Amount: Amount{Kopecks: 12345, Currency: "RUB"}}
	if d.Amount.Kopecks != 12345 || d.Amount.Currency != "RUB" {
		t.Fatalf("unexpected amount: %+v", d.Amount)
	}
}
