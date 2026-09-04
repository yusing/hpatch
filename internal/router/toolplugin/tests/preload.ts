import {plugin} from "bun";
import {fileURLToPath} from "node:url";

const sharedCorePath = fileURLToPath(new URL("../core-v1.mjs", import.meta.url));

plugin({
  name: "hpatch shared core",
  setup(build) {
    build.onResolve({filter: /^core\/v1$/u, namespace: "hpatch"}, () => ({
      path: sharedCorePath,
      namespace: "file",
    }));
  },
});
