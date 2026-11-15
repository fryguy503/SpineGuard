package epub

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

const auditPageTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<ade:template xmlns="http://www.w3.org/1999/xhtml" xmlns:ade="http://ns.adobe.com/2006/ade" xmlns:fo="http://www.w3.org/1999/XSL/Format">
<fo:layout-master-set>
 <fo:simple-page-master master-name="single_column"><fo:region-body/></fo:simple-page-master>
 <fo:simple-page-master master-name="two_column_head"><fo:region-before extent="8.3em"/><fo:region-body column-count="2" column-gap="10pt"/></fo:simple-page-master>
 <fo:page-sequence-master><fo:repeatable-page-master-alternatives>
  <fo:conditional-page-master-reference master-reference="two_column_head" page-position="first" ade:min-page-width="50em"/>
  <fo:conditional-page-master-reference master-reference="single_column"/>
 </fo:repeatable-page-master-alternatives></fo:page-sequence-master>
</fo:layout-master-set>
<ade:style><ade:styling-rule selector=".title_box" display="adobe-other-region" adobe-region="xsl-region-before"/></ade:style>
</ade:template>`

func TestPassivePageTemplateProvidesOnlySelectorProofWithoutRewriting(t *testing.T) {
	source := []byte(auditPageTemplate)
	before := append([]byte{}, source...)
	css, err := repairPageTemplateStyles(context.Background(), source)
	if err != nil || string(css) != ".title_box{}\n" {
		t.Fatalf("passive template: %q %v", css, err)
	}
	if dependent, reason := cssIDDependency(css, "calibre_pb_0"); dependent {
		t.Fatalf("class-only page template blocked an independent identifier: %s", reason)
	}
	if !bytes.Equal(source, before) {
		t.Fatal("dependency inspection rewrote the original template")
	}
	for _, selector := range []string{"#calibre_pb_0", `#calibre\5f pb_0`, "div.calibre > #calibre_pb_0", ".title_box, #calibre_pb_0"} {
		src := strings.Replace(auditPageTemplate, ".title_box", selector, 1)
		proof, err := repairPageTemplateStyles(context.Background(), []byte(src))
		if err != nil {
			t.Fatalf("known selector %q could not be inspected: %v", selector, err)
		}
		if dependent, _ := cssIDDependency(proof, "calibre_pb_0"); !dependent {
			t.Fatalf("identifier selector %q was ignored", selector)
		}
		if dependent, reason := cssIDDependency(proof, "unrelated_identifier"); dependent {
			t.Fatalf("a different identifier was blocked by %q: %s", selector, reason)
		}
	}
}

func TestPageTemplateInspectionRefusesUnsupportedOrAmbiguousFeatures(t *testing.T) {
	cases := map[string]string{
		"unknown root namespace":       strings.Replace(auditPageTemplate, repairADETemplateNS, "urn:unknown", 1),
		"foreign child":                strings.Replace(auditPageTemplate, "<ade:style>", `<ade:style><foreign xmlns="urn:other"/>`, 1),
		"foreign declaration":          strings.Replace(auditPageTemplate, "<ade:template ", `<ade:template xmlns:other="urn:other" `, 1),
		"unknown rule property":        strings.Replace(auditPageTemplate, `display="adobe-other-region"`, `display="adobe-other-region" unknown="literal"`, 1),
		"book image source":            strings.Replace(auditPageTemplate, `display="adobe-other-region"`, `src="art.png"`, 1),
		"external resource":            strings.Replace(auditPageTemplate, `display="adobe-other-region"`, `background-image="url(https://outside.invalid/art.png)"`, 1),
		"generated content":            strings.Replace(auditPageTemplate, `display="adobe-other-region"`, `content="attr(id)"`, 1),
		"dynamic layout":               strings.Replace(auditPageTemplate, `extent="8.3em"`, `extent="calc(attr(id))"`, 1),
		"unknown region":               strings.Replace(auditPageTemplate, `adobe-region="xsl-region-before"`, `adobe-region="calibre_pb_0"`, 1),
		"unsupported display":          strings.Replace(auditPageTemplate, `display="adobe-other-region"`, `display="custom(calibre_pb_0)"`, 1),
		"base URI":                     strings.Replace(auditPageTemplate, "<ade:template ", `<ade:template xml:base="other/" `, 1),
		"duplicate selector":           strings.Replace(auditPageTemplate, `selector=".title_box"`, `selector=".title_box" selector="#calibre_pb_0"`, 1),
		"aliased duplicate attributes": strings.Replace(strings.Replace(auditPageTemplate, "<ade:template ", `<ade:template xmlns:a="`+repairADETemplateNS+`" `, 1), `ade:min-page-width="50em"`, `ade:min-page-width="50em" a:min-page-width="80em"`, 1),
		"missing selector":             strings.Replace(auditPageTemplate, `selector=".title_box"`, "", 1),
		"pseudo function":              strings.Replace(auditPageTemplate, ".title_box", `:unknown(calibre_pb_0)`, 1),
		"attribute selector":           strings.Replace(auditPageTemplate, ".title_box", `[id='calibre_pb_0']`, 1),
		"selector namespace":           strings.Replace(auditPageTemplate, ".title_box", `other|thing`, 1),
		"selector rule injection":      strings.Replace(auditPageTemplate, ".title_box", `.title_box{}body`, 1),
		"empty selector":               strings.Replace(auditPageTemplate, ".title_box", " ", 1),
		"selector dangling combinator": strings.Replace(auditPageTemplate, ".title_box", ".title_box >", 1),
		"selector repeated combinator": strings.Replace(auditPageTemplate, ".title_box", ".title_box >> body", 1),
		"selector invalid escape":      strings.Replace(auditPageTemplate, ".title_box", `.title_box\`, 1),
		"CDATA rule text":              strings.Replace(auditPageTemplate, "<ade:style>", `<ade:style><![CDATA[#calibre_pb_0 {display:none}]]>`, 1),
		"text content":                 strings.Replace(auditPageTemplate, "<ade:style>", `<ade:style>some unsupported stylesheet text`, 1),
		"include":                      strings.Replace(auditPageTemplate, "<ade:style>", `<ade:style><ade:include href="another.xpgt"/>`, 1),
		"script":                       strings.Replace(auditPageTemplate, "<ade:style>", `<ade:style><script xmlns="http://www.w3.org/1999/xhtml">id</script>`, 1),
		"unknown master":               strings.Replace(auditPageTemplate, `master-reference="single_column"`, `master-reference="unknown"`, 1),
		"duplicate master":             strings.Replace(auditPageTemplate, `master-name="two_column_head"`, `master-name="single_column"`, 1),
		"column expression":            strings.Replace(auditPageTemplate, `column-count="2"`, `column-count="var(--columns)"`, 1),
		"negative size":                strings.Replace(auditPageTemplate, `column-gap="10pt"`, `column-gap="-10pt"`, 1),
		"doctype":                      strings.Replace(auditPageTemplate, "<ade:template", `<!DOCTYPE ade:template SYSTEM "https://outside.invalid/template.dtd"><ade:template`, 1),
		"stylesheet PI":                strings.Replace(auditPageTemplate, "<ade:template", `<?xml-stylesheet href="extra.css"?><ade:template`, 1),
		"legacy encoding":              strings.Replace(auditPageTemplate, `encoding="UTF-8"`, `encoding="ISO-8859-1"`, 1),
		"truncated XML":                strings.TrimSuffix(auditPageTemplate, "</ade:template>"),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := repairPageTemplateStyles(context.Background(), []byte(src)); err == nil {
				t.Fatal("unsupported template could permit an unsafe identifier edit")
			}
		})
	}
}

func TestPageTemplateInspectionIsBoundedAndCancelable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repairPageTemplateStyles(ctx, []byte(auditPageTemplate)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled inspection: %v", err)
	}
	if _, err := repairPageTemplateStyles(context.Background(), bytes.Repeat([]byte{' '}, (1<<20)+1)); err == nil {
		t.Fatal("unbounded template accepted")
	}
}
