import {plugin} from "bun";
import {fileURLToPath} from "node:url";

const sharedCorePath = fileURLToPath(new URL("../core-v1.mjs", import.meta.url));

plugin({
  name: "mekugi shared core",
  setup(build) {
    build.onResolve({filter: /^core\/v1$/u, namespace: "mekugi"}, () => ({
      path: sharedCorePath,
      namespace: "file",
    }));
  },
});
