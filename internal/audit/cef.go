package audit

import (
	"fmt"
	"sort"
	"strings"
)

// CEF форматирует запись в ArcSight CEF для выгрузки в SIEM (порт AuditSink).
// CEF:Version|Device Vendor|Device Product|Device Version|Signature ID|Name|Severity|Extension
func CEF(r Record, productVersion string) string {
	ext := []string{
		"rt=" + fmt.Sprint(r.At.UnixMilli()),
		"suser=" + cefEscapeExt(r.Actor),
		"act=" + cefEscapeExt(string(r.Action)),
		"cs1Label=objectType", "cs1=" + cefEscapeExt(r.ObjectType),
		"cs2Label=objectId", "cs2=" + cefEscapeExt(r.ObjectID),
		"cs3Label=productId", "cs3=" + cefEscapeExt(r.ProductID.String()),
		"cs4Label=hash", "cs4=" + r.Hash,
		"cn1Label=seq", "cn1=" + fmt.Sprint(r.Seq),
	}
	keys := make([]string, 0, len(r.Details))
	for k := range r.Details {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ext = append(ext, cefEscapeExt(k)+"="+cefEscapeExt(fmt.Sprint(r.Details[k])))
	}
	return fmt.Sprintf("CEF:0|Metis|Metis|%s|%s|%s|%d|%s",
		cefEscapeHeader(productVersion), cefEscapeHeader(string(r.Action)), cefEscapeHeader(string(r.Action)),
		severity(r.Action), strings.Join(ext, " "))
}

func severity(a Action) int {
	switch a {
	case ActionAccessChange, ActionRuleChange, ActionLoginDenied:
		return 7
	case ActionExport, ActionViewFinance, ActionConnectorChange:
		return 5
	default:
		return 3
	}
}

func cefEscapeHeader(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "|", `\|`)
}

func cefEscapeExt(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "=", `\=`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return strings.ReplaceAll(s, "\n", `\n`)
}
