export type CustomCarrier = {
  kind: "custom";
  name: string;
  payload: string;
};

export type FunctionCarrier = {
  kind: "function";
  name: string;
  payload: string;
};

export type ExecCarrier = {
  kind: "exec";
  template?: string;
};

export type Carrier = CustomCarrier | FunctionCarrier | ExecCarrier;

export type TranslationAPI = {
  custom(name: string, input: string): CustomCarrier;
  function(name: string, argumentsJSON: string): FunctionCarrier;
  exec(template?: string): ExecCarrier;
};

export type ExecutionOutput = {
  stdout?: string;
  stderr?: string;
  exitCode: number;
};

export type ExecutionResult = ExecutionOutput & {
  stock?: ExecutionOutput;
};

export type ExecutionContext = {
  stdinFD: number | null;
  scriptReadFD: number | null;
  scriptWriteFD: number | null;
};

export type Tool<T> = {
  specification: {
    type: "custom";
    name: string;
    description: string;
    format?: {
      type: "grammar";
      syntax: "lark" | "regex";
      definition: string;
    };
  };
  maxInputBytes: number;
  parse(input: string): T | Promise<T>;
  argv(input: T): string[] | Promise<string[]>;
  translate(input: T, api: TranslationAPI): Carrier | Promise<Carrier>;
  execute(argv: string[], context: ExecutionContext): ExecutionResult | Promise<ExecutionResult>;
};

export type Plugin = {
  apiVersion: "hpatch-tool-plugin/v1";
  id: string;
  tools: Tool<unknown>[];
};
