import { describe, expect, it } from "vitest";
import { inferKind, planArguments, renderTitle, resolvePointer, titleText } from "./tool-presentation";

describe("resolvePointer", () => {
  const root = { a: { "b/c": [10, { "~x": 1 }] }, empty: "" };
  it("walks objects, arrays, and escapes", () => {
    expect(resolvePointer(root, "/a/b~1c/1/~0x")).toEqual({ found: true, value: 1 });
    expect(resolvePointer(root, "/a/b~1c/0")).toEqual({ found: true, value: 10 });
    expect(resolvePointer(root, "")).toEqual({ found: true, value: root });
    expect(resolvePointer(root, "/empty")).toEqual({ found: true, value: "" });
  });
  it("reports absence rather than inventing a value", () => {
    expect(resolvePointer(root, "/missing").found).toBe(false);
    expect(resolvePointer(root, "/a/b~1c/9").found).toBe(false);
    expect(resolvePointer(root, "/a/b~1c/01").found).toBe(false);
    expect(resolvePointer(root, "a").found).toBe(false);
    expect(resolvePointer(null, "/a").found).toBe(false);
  });
});

describe("inferKind", () => {
  it("reads the value where it can", () => {
    expect(inferKind("body", "<div>Hey René,</div><div><br></div>")).toBe("html");
    expect(inferKind("patch", "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b")).toBe("diff");
    expect(inferKind("input", "*** Begin Patch\n*** Update File: a.go\n@@\n-a\n+b\n*** End Patch")).toBe("diff");
    expect(inferKind("site", "https://example.com/x")).toBe("url");
    expect(inferKind("acting_email", "chris@example.com")).toBe("email");
    expect(inferKind("file", "/etc/hosts")).toBe("path");
    expect(inferKind("target", "./src/main.go")).toBe("path");
    expect(inferKind("labels", ["a", "b"])).toBe("list");
    expect(inferKind("files", [{ path: "a" }])).toBe("json");
    expect(inferKind("options", { a: 1 })).toBe("json");
    expect(inferKind("n", 3)).toBe("text");
    expect(inferKind("flag", true)).toBe("text");
    expect(inferKind("x", null)).toBe("text");
  });
  it("reads the key for the rest", () => {
    expect(inferKind("command", "ls -la")).toBe("shell");
    expect(inferKind("thread_id", "1a0a01089cccfd03")).toBe("reference");
    expect(inferKind("type", "reply")).toBe("enum");
    expect(inferKind("deadline_at", "2026-09-15T12:00:00Z")).toBe("datetime");
    expect(inferKind("content", "line one\nline two")).toBe("code");
    expect(inferKind("notes", "line one\nline two")).toBe("prose");
    expect(inferKind("subject", "Thanks for the logs")).toBe("text");
    expect(inferKind("summary", "x".repeat(100))).toBe("prose");
  });
  it("does not mistake a sentence with a slash for a path", () => {
    expect(inferKind("file", "the file at /tmp/x")).toBe("text");
    expect(inferKind("type", "two words")).toBe("text");
  });
});

const draft = {
  body: "<div>Hey René,</div>",
  type: "reply",
  draft_id: "draft007e7da3e4b8d14b",
  thread_id: "1a0a01089cccfd03",
  acting_email: "chris@example.com",
  extra_flag: true,
};

describe("planArguments", () => {
  it("keeps extra and partially consumed fields within list items", () => {
    const args = { edits: [{ old: "a", next: "b", replace_all: true, "b/c": "hidden?" }] };
    const plan = planArguments(args, {
      fields: [{ path: "/edits", kind: "list", item: { kind: "diff", before: "old", after: "next" } }],
    });
    expect(plan.primary[0].value).toEqual(args.edits);
    expect(plan.primary[0].items?.[0].additional?.map((field) => [field.key, field.value])).toEqual([
      ["/edits/0/replace_all", true], ["/edits/0/b~1c", "hidden?"],
    ]);
    const nested = planArguments({ files: [{ content: { body: "hello", bcc: "x@example.com" } }] }, {
      fields: [{ path: "/files", kind: "list", item: { path: "content/body", kind: "prose" } }],
    });
    expect(nested.primary[0].items?.[0].additional?.[0].value).toEqual({ body: "hello", bcc: "x@example.com" });
  });

  it("falls back for incomplete diffs and values of the wrong type", () => {
    for (const args of [{ old: "a" }, { old: null, next: "b" }, { old: {}, next: false }]) {
      const plan = planArguments(args, { fields: [{ kind: "diff", before: "/old", after: "/next" }] });
      expect(plan.primary.map((field) => field.value)).toEqual(Object.values(args));
    }
    expect(planArguments({ patch: { delete: true } }, { fields: [{ path: "/patch", kind: "diff" }] }).primary[0])
      .toMatchObject({ kind: "json", value: { delete: true } });
    expect(planArguments({ body: null }, { fields: [{ path: "/body", kind: "html" }] }).primary[0])
      .toMatchObject({ kind: "text", value: null });
  });

  it("renders root and nested arrays without losing elements", () => {
    expect(planArguments(["one", "two"], null).primary[0].items?.map((item) => item.value)).toEqual(["one", "two"]);
    const plan = planArguments({ lists: [["one", "two"]] }, { fields: [{ path: "/lists", kind: "list" }] });
    expect(plan.primary[0].items?.[0].items?.map((item) => item.value)).toEqual(["one", "two"]);
    const partial = planArguments(["one", "two"], { fields: [{ path: "/0", kind: "text" }] });
    expect(partial.primary[1].value).toEqual(["one", "two"]);
  });

  it("renders named fields in order and every unnamed argument after them", () => {
    const plan = planArguments(draft, {
      title: "Email draft",
      fields: [
        { path: "/type", kind: "enum", label: "Type" },
        { path: "/to", kind: "list", item: { kind: "email" } },
        { path: "/body", kind: "html", label: "Body" },
        { path: "/acting_email", kind: "email", label: "From", role: "detail" },
        { path: "/thread_id", kind: "reference", role: "detail" },
      ],
    });
    expect(plan.primary.map((field) => [field.key, field.kind, field.named])).toEqual([
      ["/type", "enum", true],
      ["/body", "html", true],
      ["/draft_id", "reference", false],
      ["/extra_flag", "text", false],
    ]);
    expect(plan.primary[0].label).toBe("Type");
    expect(plan.primary[2].label).toBe("Draft id");
    expect(plan.detail.map((field) => [field.key, field.label])).toEqual([
      ["/acting_email", "From"],
      ["/thread_id", "Thread id"],
    ]);
  });

  it("renders a diff pair and keeps a partially read nested value on the page", () => {
    const args = { file_path: "/a.go", old_string: "x", new_string: "y", edits: [{ old_string: "1", new_string: "2" }, { old_string: "3", new_string: "4" }] };
    const plan = planArguments(args, {
      fields: [
        { path: "/file_path", kind: "path" },
        { kind: "diff", before: "/old_string", after: "/new_string", label: "Change" },
        { path: "/edits", kind: "list", item: { kind: "diff", before: "old_string", after: "new_string" } },
      ],
    });
    expect(plan.primary[1]).toMatchObject({ kind: "diff", before: "x", after: "y", label: "Change", named: true });
    expect(plan.primary[2].kind).toBe("list");
    expect(plan.primary[2].items?.map((item) => [item.key, item.before, item.after])).toEqual([
      ["/edits/0", "1", "2"],
      ["/edits/1", "3", "4"],
    ]);
    expect(plan.primary).toHaveLength(3);
  });

  it("skips a field whose path this call does not carry", () => {
    const plan = planArguments({ command: "ls" }, {
      fields: [{ path: "/command", kind: "shell" }, { path: "/description", kind: "prose" }, { kind: "diff", before: "/a", after: "/b" }],
    });
    expect(plan.primary.map((field) => field.key)).toEqual(["/command"]);
  });

  it("renders by shape without a presentation, and a non-object payload as one value", () => {
    expect(planArguments(draft, null).primary.map((field) => field.kind)).toEqual(["html", "enum", "reference", "reference", "email", "text"]);
    expect(planArguments("rm -rf /", null).primary).toEqual([{ key: "", label: "Arguments", kind: "text", value: "rm -rf /", named: false }]);
    expect(planArguments({}, null)).toEqual({ primary: [], detail: [] });
  });

  it("leaves out hidden top-level arguments unless the presentation names them", () => {
    const args = { query: "hello", withhuman_scope: "read" };
    expect(planArguments(args, null, ["withhuman_scope"]).primary.map((field) => field.key)).toEqual(["/query"]);
    const named = planArguments(args, { fields: [{ path: "/withhuman_scope", kind: "text", label: "Scope" }] }, ["withhuman_scope"]);
    expect(named.primary.map((field) => field.key)).toEqual(["/withhuman_scope", "/query"]);
  });

  it("renders a bare list by the shape of its elements", () => {
    const plan = planArguments({ to: ["a@x.com", "b@x.com"], n: [1, 2] }, null);
    expect(plan.primary[0].items?.map((item) => [item.kind, item.value])).toEqual([["email", "a@x.com"], ["email", "b@x.com"]]);
    expect(plan.primary[1].items?.map((item) => item.label)).toEqual(["1", "2"]);
  });
});

describe("renderTitle", () => {
  it("interpolates scalar arguments and marks them", () => {
    const segments = renderTitle("Edit {{/file_path}} in {{/repo}}", { file_path: "a.go", repo: "core" });
    expect(segments).toEqual([
      { kind: "text", text: "Edit " },
      { kind: "value", text: "a.go" },
      { kind: "text", text: " in " },
      { kind: "value", text: "core" },
    ]);
    expect(titleText(segments!)).toBe("Edit a.go in core");
  });
  it("drops a missing placeholder cleanly and falls back when none resolve", () => {
    expect(titleText(renderTitle("Edit {{/file_path}} in {{/repo}}", { repo: "core" })!)).toBe("Edit in core");
    expect(renderTitle("Edit {{/file_path}}", { file_path: { nested: true } })).toBeNull();
    expect(renderTitle("Edit {{/file_path}}", {})).toBeNull();
    expect(renderTitle("", {})).toBeNull();
    expect(renderTitle("Run a shell command", {})).toEqual([{ kind: "text", text: "Run a shell command" }]);
  });
  it("caps a long value", () => {
    const segments = renderTitle("Write {{/path}}", { path: "x".repeat(100) });
    expect(segments![1].text).toHaveLength(60);
    expect(segments![1].text.endsWith("…")).toBe(true);
  });
});
