package gogmoon

import "testing"

func TestExtractCommandStopsAtMailSignature(t *testing.T) {
	body := "pwd;\u00a0datetime\n\n--\nOnt Rif\nОтправлено из Почты Mail ( https://trk.mail.ru/c/zzm979 )"

	got := ExtractCommand(body)
	want := "pwd; datetime"
	if got != want {
		t.Fatalf("ExtractCommand() = %q, want %q", got, want)
	}
}

func TestExtractCommandConvertsHTMLToCommandLines(t *testing.T) {
	body := `<html><body><div>pwd</div><div>ls -la</div><blockquote><div>old command</div></blockquote></body></html>`

	got := ExtractCommand(body)
	want := "pwd\nls -la"
	if got != want {
		t.Fatalf("ExtractCommand() = %q, want %q", got, want)
	}
}

func TestExtractCommandStopsAtHTMLSignature(t *testing.T) {
	body := `<html><body><div>cat logs.txt | grep 'http server'</div><div data-signature-widget="container"><div>--<br>Ont Rif</div></div></body></html>`

	got := ExtractCommand(body)
	want := "cat logs.txt | grep 'http server'"
	if got != want {
		t.Fatalf("ExtractCommand() = %q, want %q", got, want)
	}
}

func TestExtractCommandKeepsShellRedirectionLikeText(t *testing.T) {
	body := `printf '<not-html>' > output.txt`

	got := ExtractCommand(body)
	want := `printf '<not-html>' > output.txt`
	if got != want {
		t.Fatalf("ExtractCommand() = %q, want %q", got, want)
	}
}
