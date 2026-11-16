// Optional pinned-host contract check; runtime plugin stays dependency-free.
// DSH_TOOLS_SDK=/absolute/node_modules/@deepseek-ai/dsh-tools/lib/index.js node test/host-contract.mjs
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { createTools } from "../src/index.mjs";
const sdk = await import(pathToFileURL(process.env.DSH_TOOLS_SDK).href);
for (const d of createTools({ url: "https://localhost", token: "test" })) {
  sdk.assertObjectJsonSchema(d.parameters);
  sdk.assertSupportedJsonSchema(d.output.schema);
  assert.equal(typeof d.output.render, "function");
  assert.equal(typeof d.execute, "function");
}
console.log("Pinned DSH SDK: all eight registry-ready tool schemas PASS");
