package webui

import (
	"os/exec"
	"strings"
	"testing"
)

// Run the real selection updater without rebuilding the tree: changing innerHTML
// would discard the scroll container and the checkbox that currently has focus.
func TestAddFileSelectionKeepsTree(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	b, err := files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, "  function fileTree(e)")
	end := strings.Index(src, "  function renderAdFiles(")
	if start < 0 || end <= start {
		t.Fatal("add file selection functions not found")
	}
	script := `
const assert = require("node:assert/strict");
const e = { kind: "file", name: "test", files: [
 {path:"root/a",size:10}, {path:"root/sub/b",size:20},
 {path:"root/sub/c",size:30}, {path:"root/pad",size:99,pad:true}
], sel:[true,true,true,true] };
const boxes = [0,1,2].map(i => ({dataset:{file:String(i)}, checked:true}));
const folders = ["root/", "root/sub/"].map(path => ({dataset:{dir:path},checked:true,indeterminate:false}));
const summary = {textContent:""};
const tree = {scrollTop:840};
const focused = boxes[2];
const box = {
 set innerHTML(value) { throw new Error("selection must not replace the DOM"); },
 querySelectorAll(sel) { return sel === "input[data-file]" ? boxes : folders; },
 querySelector(sel) { return sel === ".tree" ? tree : summary; }
};
const $ = () => box, adCur = () => e, bytes = n => n + " B";
` + src[start:end] + `
updateAdFileSelection();
assert.equal(summary.textContent,"Выбрано 3 из 3 · 60 B");
e.sel[2] = false;
updateAdFileSelection();
assert.equal(boxes[2].checked,false);
assert.ok(folders.every(c => c.indeterminate && !c.checked && c.dataset.state === "some"));
assert.equal(summary.textContent,"Выбрано 2 из 3 · 30 B");
// A folder checkbox changes all descendants, including ones currently folded.
e.sel[1] = e.sel[2] = false;
updateAdFileSelection();
assert.equal(folders[1].dataset.state,"none");
assert.equal(folders[0].dataset.state,"some");
e.sel.fill(false); updateAdFileSelection();
assert.equal(summary.textContent,"Выбрано 0 из 3 · 0 B");
assert.ok([...boxes,...folders].every(c => !c.checked && !c.indeterminate));
e.sel.fill(true); updateAdFileSelection();
assert.ok([...boxes,...folders].every(c => c.checked && !c.indeterminate));
assert.equal(tree.scrollTop,840);
assert.equal(boxes[2],focused);
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("selection updater: %v\n%s", err, out)
	}
}
