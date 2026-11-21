package brain

import (
	"encoding/json"
	"strings"

	"cosmos/core/internal/mcp"
)

// The schemas are written out as JSON text, not built from Go maps, because
// field order matters. llama.cpp turns a schema into a grammar that makes
// the model write the fields in this order, and a small model does much
// better when it names the tool before its arguments, or gives its reason
// before its verdict. A Go map would sort the fields alphabetically.

// Each tool has its own shape with every field required, so the grammar
// itself stops the model putting a date in the wrong field. Fields that do
// not apply are left as "" or 0.
const stepShapes = `{"anyOf":[
 {"type":"object","properties":{"tool":{"const":"weather.now"},"place":{"type":"string"}},"required":["tool","place"]},
 {"type":"object","properties":{"tool":{"const":"memory.search"},"query":{"type":"string"}},"required":["tool","query"]},
 {"type":"object","properties":{"tool":{"const":"people.find"},"name":{"type":"string"}},"required":["tool","name"]},
 {"type":"object","properties":{"tool":{"const":"calendar.list"},"from":{"type":"string"},"to":{"type":"string"}},"required":["tool","from","to"]},
 {"type":"object","properties":{"tool":{"const":"calendar.find"},"query":{"type":"string"}},"required":["tool","query"]},
 {"type":"object","properties":{"tool":{"const":"calendar.add"},"title":{"type":"string"},"date":{"type":"string"},"time":{"type":"string"},"duration_min":{"type":"integer"},"location":{"type":"string"},"kind":{"type":"string","enum":["event","reminder"]},"repeat":{"type":"string","enum":["none","daily","weekly","monthly","yearly"]}},"required":["tool","title","date","time","duration_min","location","kind","repeat"]},
 {"type":"object","properties":{"tool":{"const":"calendar.remove"},"id":{"type":"integer"}},"required":["tool","id"]},
 {"type":"object","properties":{"tool":{"const":"web.search"},"query":{"type":"string","maxLength":120}},"required":["tool","query"]},
 {"type":"object","properties":{"tool":{"const":"web.read"},"url":{"type":"string","maxLength":300}},"required":["tool","url"]},
 {"type":"object","properties":{"tool":{"const":"podcasts.search"},"query":{"type":"string","maxLength":120}},"required":["tool","query"]},
 {"type":"object","properties":{"tool":{"const":"podcasts.listen"},"query":{"type":"string","maxLength":120}},"required":["tool","query"]},
 {"type":"object","properties":{"tool":{"const":"lists.add"},"list":{"type":"string","maxLength":40},"items":{"type":"array","minItems":1,"maxItems":12,"items":{"type":"string","maxLength":60}}},"required":["tool","list","items"]},
 {"type":"object","properties":{"tool":{"const":"lists.remove"},"list":{"type":"string","maxLength":40},"items":{"type":"array","minItems":1,"maxItems":12,"items":{"type":"string","maxLength":60}}},"required":["tool","list","items"]},
 {"type":"object","properties":{"tool":{"const":"lists.show"},"list":{"type":"string","maxLength":40}},"required":["tool","list"]},
 {"type":"object","properties":{"tool":{"const":"lists.done"},"list":{"type":"string","maxLength":40},"items":{"type":"array","minItems":1,"maxItems":12,"items":{"type":"string","maxLength":60}}},"required":["tool","list","items"]}
]}`

var planSchema = planSchemaWith(stepShapes)

func planSchemaWith(shapes string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
 "intent":{"type":"string","enum":["chat","weather","calendar","memory","people","web","lists","podcasts","other"]},
 "mode":{"type":"string","enum":["once","steps"]},
 "steps":{"type":"array","maxItems":4,"items":` + shapes + `}},
 "required":["intent","mode","steps"]}`)
}

func nextSchemaWith(shapes string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
 "done":{"type":"boolean"},
 "step":` + shapes + `},
 "required":["done","step"]}`)
}

// shapesWith adds one shape per connected tool to the built-in ones. The
// tool's name comes first, then its arguments in the form the tool
// itself describes.
func shapesWith(tools []mcp.Tool) string {
	if len(tools) == 0 {
		return stepShapes
	}
	base := strings.TrimSuffix(strings.TrimSpace(stepShapes), "]}")
	var sb strings.Builder
	sb.WriteString(strings.TrimRight(base, "\n "))
	for _, t := range tools {
		name, _ := json.Marshal(t.ID())
		args, _ := json.Marshal(mcp.Shape(t.Schema))
		sb.WriteString("," + ` {"type":"object","properties":{"tool":{"const":` + string(name) + `},"args":` + string(args) + `},"required":["tool","args"]}`)
	}
	sb.WriteString("\n]}")
	return sb.String()
}

// done comes first: when nothing more is needed the model says so and
// fills in a harmless empty step.
var nextSchema = json.RawMessage(`{"type":"object","properties":{
 "done":{"type":"boolean"},
 "step":` + stepShapes + `},
 "required":["done","step"]}`)

var noticeSchema = json.RawMessage(`{"type":"object","properties":{
 "touches":{"type":"string","maxLength":80},
 "emotion":{"type":"string","enum":["calm","glad","warmth","tenderness","amusement","curiosity","hope","pride","concern","worry","sadness","hurt","unease","anger"]},
 "intensity":{"type":"number"},
 "importance":{"type":"integer"},
 "follow_up":{"type":"object","properties":{
   "when":{"type":"string","enum":["none","today","tonight","tomorrow","this week"]},
   "text":{"type":"string","maxLength":160},
   "reason":{"type":"string","maxLength":160}},
  "required":["when","text","reason"]},
 "resolved":{"type":"array","maxItems":5,"items":{"type":"integer"}}},
 "required":["touches","emotion","intensity","importance","follow_up","resolved"]}`)

// The reason is written before the verdict, so the verdict follows from it.
var verdictSchema = json.RawMessage(`{"type":"object","properties":{
 "reason":{"type":"string","maxLength":240},
 "ok":{"type":"boolean"}},
 "required":["reason","ok"]}`)

// String lengths are capped. Without a cap a small model can start a
// note and never finish it, running out of tokens mid-JSON and losing
// the whole reply, including the parts that were fine.
var claimsSchema = json.RawMessage(`{"type":"object","properties":{
 "invented":{"type":"string","maxLength":160}},
 "required":["invented"]}`)

var captureSchema = json.RawMessage(`{"type":"object","properties":{
 "changes":{"type":"array","maxItems":5,"items":{"type":"object","properties":{
   "op":{"type":"string","enum":["add","update"]},
   "ref":{"type":"integer"},
   "category":{"type":"string","enum":["personal","family","work","health","preferences","general"]},
   "content":{"type":"string","maxLength":200}},
  "required":["op","ref","category","content"]}},
 "people":{"type":"array","maxItems":5,"items":{"type":"object","properties":{
   "name":{"type":"string","maxLength":40},
   "relationship":{"type":"string","maxLength":40},
   "note":{"type":"string","maxLength":140}},
  "required":["name","relationship","note"]}}},
 "required":["changes","people"]}`)

var podNotesSchema = json.RawMessage(`{"type":"object","properties":{
 "notes":{"type":"array","maxItems":5,"items":{"type":"string","maxLength":260}}},
 "required":["notes"]}`)

var podThinkSchema = json.RawMessage(`{"type":"object","properties":{
 "summary":{"type":"string","maxLength":600},
 "takeaways":{"type":"array","maxItems":8,"items":{"type":"string","maxLength":260}},
 "thoughts":{"type":"array","maxItems":3,"items":{"type":"string","maxLength":300}},
 "share":{"type":"string","maxLength":260}},
 "required":["summary","takeaways","thoughts","share"]}`)

var titleSchema = json.RawMessage(`{"type":"object","properties":{
 "title":{"type":"string","maxLength":48}},
 "required":["title"]}`)

var nicknameSchema = json.RawMessage(`{"type":"object","properties":{
 "nicknames":{"type":"array","maxItems":5,"items":{"type":"object","properties":{
   "line":{"type":"string","maxLength":160},
   "nickname":{"type":"string","maxLength":30},
   "for":{"type":"string","enum":["companion","person","user"]},
   "person":{"type":"string","maxLength":40}},
  "required":["line","nickname","for","person"]}}},
 "required":["nicknames"]}`)

var reflectSchema = json.RawMessage(`{"type":"object","properties":{
 "journal":{"type":"string"},
 "reflections":{"type":"array","maxItems":3,"items":{"type":"object","properties":{
   "evidence":{"type":"array","minItems":1,"maxItems":4,"items":{"type":"integer"}},
   "text":{"type":"string","maxLength":240},
   "importance":{"type":"integer"}},
  "required":["evidence","text","importance"]}},
 "share":{"type":"string"},
 "wonder":{"type":"array","maxItems":2,"items":{"type":"object","properties":{
   "question":{"type":"string","maxLength":160},
   "why":{"type":"string","maxLength":160}},
  "required":["question","why"]}},
 "proposals":{"type":"array","maxItems":3,"items":{"type":"object","properties":{
   "kind":{"type":"string","enum":["value","writing","planning"]},
   "target":{"type":"string"},
   "reason":{"type":"string"},
   "after":{"type":"string"}},
  "required":["kind","target","reason","after"]}}},
 "required":["journal","reflections","share","wonder","proposals"]}`)

var tidySchema = json.RawMessage(`{"type":"object","properties":{
 "merge":{"type":"array","maxItems":5,"items":{"type":"object","properties":{
   "keep":{"type":"integer"},
   "drop":{"type":"array","items":{"type":"integer"}},
   "content":{"type":"string"}},
  "required":["keep","drop","content"]}},
 "archive":{"type":"array","maxItems":5,"items":{"type":"object","properties":{
   "ref":{"type":"integer"},
   "why":{"type":"string"}},
  "required":["ref","why"]}}},
 "required":["merge","archive"]}`)
