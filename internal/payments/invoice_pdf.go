package payments

import (
	"bytes"
	_ "embed"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

//go:embed invoicefonts/LiberationSans-Regular.ttf
var invoiceRegularFont []byte

//go:embed invoicefonts/LiberationSans-Bold.ttf
var invoiceBoldFont []byte

// RenderInvoicePDF reproduces the supplied A4 layout without launching a
// browser, evaluating HTML or accessing any network or filesystem resource.
func RenderInvoicePDF(p Payment, number string, issued time.Time) ([]byte, error) {
	if p.VAT == nil || p.Billing == nil || p.InvoiceMode != "local" {
		return nil, ErrUnavailable
	}
	f := fpdf.New("P", "mm", "A4", "")
	f.SetMargins(20, 18, 20)
	f.SetAutoPageBreak(false, 18)
	f.SetCellMargin(0)
	f.SetCreationDate(issued)
	f.SetModificationDate(issued)
	f.SetTitle("Invoice "+number, true)
	f.SetAuthor(p.VAT.Seller.LegalName, true)
	f.AddUTF8FontFromBytes("Invoice", "", invoiceRegularFont)
	f.AddUTF8FontFromBytes("Invoice", "B", invoiceBoldFont)
	f.SetFont("Invoice", "", 10)
	f.SetTextColor(32, 35, 33)
	f.AddPage()
	font := func(bold bool, size float64) {
		style := ""
		if bold {
			style = "B"
		}
		f.SetFont("Invoice", style, size)
	}
	// Respect the template's 28 mm labels, 4 mm pair gap and 2 mm row gap.
	text := func(x, y, w float64, value string, bold bool) float64 {
		font(bold, 10)
		f.SetXY(x, y)
		f.MultiCell(w, 5.3, value, "", "L", false)
		return f.GetY()
	}
	nextPage := func() {
		if f.PageNo() < f.PageCount() {
			f.SetPage(f.PageNo() + 1)
		} else {
			f.AddPage()
		}
	}
	pair := func(x, y, w float64, label, value string, bold bool) float64 {
		font(bold, 10)
		lines := f.SplitText(value, w-32)
		if len(lines) == 0 {
			lines = []string{""}
		}
		for len(lines) > 0 {
			if y+5.3 > 279 {
				nextPage()
				y = 18
			}
			count := int((279 - y) / 5.3)
			if count > len(lines) {
				count = len(lines)
			}
			f.SetTextColor(98, 104, 98)
			if bold {
				f.SetTextColor(32, 35, 33)
			}
			f.SetXY(x, y)
			f.MultiCell(28, 5.3, label, "", "L", false)
			f.SetTextColor(32, 35, 33)
			text(x+32, y, w-32, strings.Join(lines[:count], "\n"), bold)
			y += float64(count) * 5.3
			lines = lines[count:]
			label = ""
		}
		return y + 2
	}
	font(false, 32)
	f.SetXY(20, 18)
	f.CellFormat(120, 12, "Invoice", "", 0, "L", false, 0, "")
	y := 36.5
	y = pair(20, y, 120, "Number", number, false)
	y = pair(20, y, 120, "Issue date", issued.Format("02 Jan 2006"), false)
	y = pair(20, y, 120, "Due date", p.RequestExpiry().Format("02 Jan 2006 15:04 UTC"), false)
	if !p.Live {
		y = pair(20, y, 120, "Mode", "Test payment", true)
	}
	if logo := p.VAT.Seller.InvoiceLogo; logo != nil {
		normalized, e := NormalizeInvoiceLogo(logo)
		if e != nil {
			return nil, e
		}
		info := f.RegisterImageOptionsReader("seller-logo", fpdf.ImageOptions{ImageType: "PNG", ReadDpi: false}, bytes.NewReader(normalized.Data))
		if info == nil {
			return nil, ErrInvalid
		}
		w, h := info.Extent()
		scale := math.Min(36/w, 36/h)
		w *= scale
		h *= scale
		f.ImageOptions("seller-logo", 190-w, 18, w, h, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	}
	y = math.Max(y, 54) + 16
	seller := p.VAT.Seller
	bill := p.Billing
	partyPage := f.PageNo()
	left := pair(20, y, 79, "From", seller.LegalName, true)
	if seller.RegistrationNumber != "" {
		left = pair(20, left, 79, "Reg. number", seller.RegistrationNumber, false)
	}
	if seller.VATNumber != "" {
		left = pair(20, left, 79, "Tax ID", seller.VATNumber, false)
	}
	left = pair(20, left, 79, "Address", invoiceAddress(seller.Address), false)
	if seller.Email != "" {
		left = pair(20, left, 79, "Email", seller.Email, false)
	}
	leftPage := f.PageNo()
	f.SetPage(partyPage)
	right := pair(111, y, 79, "Bill to", bill.BusinessName, true)
	for _, id := range bill.TaxIDs {
		if id.Value != "" {
			right = pair(111, right, 79, "Tax ID", id.Value, false)
		}
	}
	right = pair(111, right, 79, "Address", invoiceAddress(bill.Address), false)
	if bill.Email != "" {
		right = pair(111, right, 79, "Email", bill.Email, false)
	}
	rightPage := f.PageNo()
	if leftPage > rightPage {
		f.SetPage(leftPage)
		y = left + 14
	} else if rightPage > leftPage {
		y = right + 14
	} else {
		y = math.Max(left, right) + 14
	}
	// Long company names and addresses may need another page. Never clip them or
	// allow a table/payment footer to overwrite preceding content.
	ensure := func(height float64) {
		if y+height > 279 {
			f.AddPage()
			y = 18
		}
	}
	ensure(44)
	font(true, 10)
	f.SetXY(20, y)
	f.CellFormat(126, 5.3, "Description", "", 0, "L", false, 0, "")
	f.CellFormat(44, 5.3, "Amount, USD", "", 0, "R", false, 0, "")
	y += 7.3
	fee := int64(0)
	if p.Stablecoin != nil {
		fee = p.Stablecoin.FeeMicros
	}
	row := func(description, amount string) {
		end := text(20, y, 125, description, false)
		font(false, 10)
		f.SetXY(146, y)
		f.CellFormat(44, 5.3, amount, "", 0, "R", false, 0, "")
		y = math.Max(end, y+5.3) + 2
	}
	row("Balance top-up for content generation", invoiceMoney(p.AmountCents*10000, false))
	if p.Stablecoin != nil {
		row("Payment processing fee", invoiceMoney(fee, fee%10000 != 0))
	}
	y += 1
	ensure(48)
	total := func(label, value string, bold bool) {
		font(bold, 10)
		f.SetXY(102, y)
		f.SetTextColor(98, 104, 98)
		if bold {
			f.SetTextColor(32, 35, 33)
		}
		f.CellFormat(36, 5.3, label, "", 0, "R", false, 0, "")
		f.SetTextColor(32, 35, 33)
		f.CellFormat(52, 5.3, value, "", 0, "R", false, 0, "")
		y += 7.3
	}
	total("Subtotal", invoiceMoney(p.AmountCents*10000+fee, fee%10000 != 0), false)
	rate := p.VAT.Rule.Rate
	label := "VAT"
	if p.VAT.Rule.Treatment == "standard" {
		label = fmt.Sprintf("VAT %d.%02d%%", rate/100, rate%100)
	}
	total(label, invoiceMoney(bill.TaxCents*10000, false), false)
	total("Total due", invoiceMoney(bill.TotalCents*10000+fee, fee%10000 != 0)+" USD", true)
	note := map[string]string{"standard": "VAT applies to this supply.", "reverse_charge": "Reverse charge: the customer accounts for VAT.", "exempt": "VAT exempt.", "outside_scope": "Outside the scope of VAT."}[p.VAT.Rule.Treatment]
	y = text(20, y+3, 170, note, false)
	// Payment details sit at the foot of a short invoice, as in the HTML sample.
	footerHeight := float64(45)
	if p.Stablecoin != nil {
		footerHeight = 67.3
		if p.Settled {
			footerHeight = 52.3
		}
	}
	if p.Settled {
		footerHeight += 7.3
	}
	y = math.Max(y+12, 279-footerHeight)
	ensure(footerHeight)
	font(true, 10)
	f.SetXY(20, y)
	f.CellFormat(170, 5.3, "Payment details", "", 0, "L", false, 0, "")
	y += 8.3
	if p.Settled {
		y = pair(20, y, 170, "Status", "Paid", true)
	}
	if s := p.Stablecoin; s != nil {
		network, _, e := StablecoinAsset(s.Network, s.Token)
		if e != nil {
			return nil, e
		}
		y = pair(20, y, 170, "Method", "Stablecoins", false)
		amountLabel := "Payment amount"
		if p.Settled {
			amountLabel = "Amount received"
		}
		y = pair(20, y, 170, amountLabel, s.Amount+" "+s.Token, false)
		y = pair(20, y, 170, "Exchange rate", "1 USD = 1 "+s.Token, false)
		y = pair(20, y, 170, "Network", network.Name, false)
		y = pair(20, y, 170, "Wallet address", s.Address, false)
		if !p.Settled {
			ensure(20)
			y = text(20, y+2, 170, "Send the exact amount without rounding on the specified network. Pay withdrawal fees separately. After the due date, create a new payment.", false)
		}
	} else {
		y = pair(20, y, 170, "Method", "Bank card via Stripe", false)
		if !p.Settled {
			paymentURL := p.InvoicePaymentURL
			if paymentURL == "" {
				paymentURL = p.CheckoutURL
			}
			u, e := url.Parse(paymentURL)
			if e != nil || u.Scheme != "https" || u.Host == "" || (p.InvoicePaymentURL == "" && u.Host != "checkout.stripe.com") || u.User != nil {
				return nil, ErrConflict
			}
			font(false, 10)
			f.SetXY(20, y)
			f.SetTextColor(98, 104, 98)
			f.CellFormat(28, 5.3, "Pay online", "", 0, "L", false, 0, "")
			f.SetTextColor(32, 35, 33)
			f.SetXY(52, y)
			f.SetFont("Invoice", "U", 10)
			f.SetTextColor(24, 90, 180)
			f.WriteLinkString(5.3, "Pay this invoice securely with Stripe", paymentURL)
			font(false, 10)
			f.SetTextColor(32, 35, 33)
			y += 7.3
			y = text(20, y, 170, "Open the payment link to continue to Stripe. Sign in if requested. After the due date, create a new payment.", false)
		}
	}
	if f.Err() {
		return nil, f.Error()
	}
	var out bytes.Buffer
	if e := f.Output(&out); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func invoiceAddress(a Address) string {
	lines := []string{a.Line1}
	if a.Line2 != "" {
		lines = append(lines, a.Line2)
	}
	local := strings.TrimSpace(strings.Join([]string{a.PostalCode, a.City, a.State}, " "))
	if local != "" {
		lines = append(lines, local)
	}
	lines = append(lines, a.Country)
	return strings.Join(lines, "\n")
}
func invoiceMoney(micros int64, precise bool) string {
	if precise {
		return StablecoinAmount(micros)
	}
	return fmt.Sprintf("%d.%02d", micros/1000000, (micros%1000000)/10000)
}
