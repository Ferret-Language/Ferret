console.log("Runtime module loaded.");

export type FerretRuntimeOptions = {
  onPrint?: (text: string) => void;
  onEvent?: (event: { type: "output" | "input"; text: string }) => void;
  input?: string;
  throwOnInputNeeded?: boolean;
};

export function createFerretRuntime(options: FerretRuntimeOptions = {}) {
  let memory: WebAssembly.Memory | null = null;
  let heapPtr = 0;
  const decoder = new TextDecoder("utf-8");
  const encoder = new TextEncoder();

  const emit = options.onPrint || ((text: string) => console.log(text));
  const emitEvent = options.onEvent;
  const throwOnInputNeeded = options.throwOnInputNeeded ?? false;
  const rawInput = options.input ?? "";
  const inputLines =
    rawInput.length > 0 ? rawInput.replace(/\r\n/g, "\n").split("\n") : [];
  let inputIndex = 0;

  function align(value: number, alignment: number): number {
    const mask = alignment - 1;
    return (value + mask) & ~mask;
  }

  function bind(instance: WebAssembly.Instance) {
    memory = instance.exports.memory as WebAssembly.Memory;
    const dataEndExport = instance.exports.__data_end as any;
    const dataEnd =
      dataEndExport && typeof dataEndExport === "object"
        ? Number(dataEndExport.value)
        : Number(dataEndExport || 0);
    heapPtr = align(dataEnd, 8);
  }

  function view(): DataView {
    if (!memory) {
      throw new Error("Ferret runtime not bound to memory");
    }
    return new DataView(memory.buffer);
  }

  function readCString(ptr: number): string {
    const bytes: number[] = [];
    const mem = new Uint8Array(memory!.buffer);
    let i = ptr >>> 0;
    while (i < mem.length) {
      const b = mem[i++];
      if (b === 0) break;
      bytes.push(b);
    }
    return decoder.decode(new Uint8Array(bytes));
  }

  function writeCString(value: string): number {
    const bytes = encoder.encode(value);
    const addr = ferret_alloc(bytes.length + 1);
    const mem = new Uint8Array(memory!.buffer);
    mem.set(bytes, addr);
    mem[addr + bytes.length] = 0;
    return addr >>> 0;
  }

  function nextInputLine(): string | null {
    if (inputIndex >= inputLines.length) {
      return null;
    }
    const line = inputLines[inputIndex];
    inputIndex += 1;
    return line;
  }

  function resultStrStr(
    okValue: string | null,
    errValue: string | null,
  ): number {
    const ptr = ferret_alloc(8);
    const dv = view();
    const payload = okValue ?? errValue ?? "";
    dv.setUint32(ptr + 0, writeCString(payload), true);
    dv.setUint8(ptr + 4, okValue === null ? 1 : 0);
    return ptr >>> 0;
  }

  function resultStrI32(
    okValue: number | null,
    errValue: string | null,
  ): number {
    const ptr = ferret_alloc(8);
    const dv = view();
    if (okValue === null) {
      dv.setUint32(ptr + 0, writeCString(errValue ?? ""), true);
      dv.setUint8(ptr + 4, 1);
    } else {
      dv.setInt32(ptr + 0, okValue | 0, true);
      dv.setUint8(ptr + 4, 0);
    }
    return ptr >>> 0;
  }

  function resultStrF64(
    okValue: number | null,
    errValue: string | null,
  ): number {
    const ptr = ferret_alloc(12);
    const dv = view();
    if (okValue === null) {
      dv.setUint32(ptr + 0, writeCString(errValue ?? ""), true);
      dv.setUint8(ptr + 8, 1);
    } else {
      dv.setFloat64(ptr + 0, okValue, true);
      dv.setUint8(ptr + 8, 0);
    }
    return ptr >>> 0;
  }

  function ferret_alloc(size: number) {
    const bytes = Number(size);
    const addr = heapPtr;
    heapPtr = align(heapPtr + bytes, 8);
    return addr >>> 0;
  }

  function ferret_memcpy(dst: number, src: number, size: number) {
    const bytes = Number(size);
    const mem = new Uint8Array(memory!.buffer);
    mem.copyWithin(dst >>> 0, src >>> 0, (src >>> 0) + bytes);
  }

  function ferret_optional_unwrap_or(
    optPtr: number,
    defaultPtr: number,
    outPtr: number,
    valSize: number | bigint,
  ) {
    if (!outPtr) {
      return;
    }
    const bytes = Number(valSize);
    if (bytes <= 0) {
      return;
    }
    const mem = new Uint8Array(memory!.buffer);
    const out = outPtr >>> 0;
    if (!optPtr) {
      if (defaultPtr) {
        const def = defaultPtr >>> 0;
        mem.copyWithin(out, def, def + bytes);
      } else {
        mem.fill(0, out, out + bytes);
      }
      return;
    }
    const opt = optPtr >>> 0;
    const flag = mem[opt + bytes];
    if (flag) {
      mem.copyWithin(out, opt, opt + bytes);
    } else if (defaultPtr) {
      const def = defaultPtr >>> 0;
      mem.copyWithin(out, def, def + bytes);
    } else {
      mem.fill(0, out, out + bytes);
    }
  }

  function ferret_array_new(elemSize: number, cap: number) {
    const elemBytes = Number(elemSize);
    const capacity = Number(cap);
    const dataSize = elemBytes * capacity;
    const dataPtr = dataSize > 0 ? ferret_alloc(dataSize) : 0;
    const arrPtr = ferret_alloc(16);
    const dv = view();
    dv.setUint32(arrPtr + 0, dataPtr, true);
    dv.setInt32(arrPtr + 4, 0, true);
    dv.setInt32(arrPtr + 8, capacity, true);
    dv.setUint32(arrPtr + 12, elemBytes, true);
    return arrPtr >>> 0;
  }

  function ferret_array_clone(arrPtr: number) {
    if (!arrPtr) {
      return 0;
    }
    const dv = view();
    const dataPtr = dv.getUint32(arrPtr + 0, true);
    const length = dv.getInt32(arrPtr + 4, true);
    const capacity = dv.getInt32(arrPtr + 8, true);
    const elemSize = dv.getUint32(arrPtr + 12, true);

    const dataSize = elemSize * capacity;
    const newDataPtr = dataSize > 0 ? ferret_alloc(dataSize) : 0;
    if (dataPtr && length > 0) {
      const mem = new Uint8Array(memory!.buffer);
      mem.copyWithin(newDataPtr, dataPtr, dataPtr + length * elemSize);
    }

    const newArrPtr = ferret_alloc(16);
    dv.setUint32(newArrPtr + 0, newDataPtr, true);
    dv.setInt32(newArrPtr + 4, length, true);
    dv.setInt32(newArrPtr + 8, capacity, true);
    dv.setUint32(newArrPtr + 12, elemSize, true);
    return newArrPtr >>> 0;
  }

  function ferret_array_append(arrPtr: number, elemPtr: number) {
    const dv = view();
    let dataPtr = dv.getUint32(arrPtr + 0, true);
    const length = dv.getInt32(arrPtr + 4, true);
    let capacity = dv.getInt32(arrPtr + 8, true);
    const elemSize = dv.getUint32(arrPtr + 12, true);
    if (length >= capacity) {
      const newCap = capacity > 0 ? capacity * 2 : 1;
      const newSize = elemSize * newCap;
      const newDataPtr = newSize > 0 ? ferret_alloc(newSize) : 0;
      if (dataPtr && length > 0) {
        const mem = new Uint8Array(memory!.buffer);
        mem.copyWithin(newDataPtr, dataPtr, dataPtr + length * elemSize);
      }
      dataPtr = newDataPtr;
      capacity = newCap;
      dv.setUint32(arrPtr + 0, dataPtr, true);
      dv.setInt32(arrPtr + 8, capacity, true);
    }
    const dest = dataPtr + length * elemSize;
    ferret_memcpy(dest, elemPtr, elemSize);
    dv.setInt32(arrPtr + 4, length + 1, true);
    return 1;
  }

  function ferret_array_get(arrPtr: number, index: number) {
    const dv = view();
    const dataPtr = dv.getUint32(arrPtr + 0, true);
    const length = dv.getInt32(arrPtr + 4, true);
    const elemSize = dv.getUint32(arrPtr + 12, true);
    if (index < 0 || index >= length) {
      return 0;
    }
    return (dataPtr + index * elemSize) >>> 0;
  }

  function ferret_array_set(arrPtr: number, index: number, elemPtr: number) {
    const dv = view();
    const dataPtr = dv.getUint32(arrPtr + 0, true);
    const length = dv.getInt32(arrPtr + 4, true);
    const elemSize = dv.getUint32(arrPtr + 12, true);
    if (index < 0 || index >= length) {
      return 0;
    }
    ferret_memcpy(dataPtr + index * elemSize, elemPtr, elemSize);
    return 1;
  }

  function ferret_array_len(arrPtr: number) {
    const dv = view();
    return dv.getInt32(arrPtr + 4, true);
  }

  function ferret_array_cap(arrPtr: number) {
    const dv = view();
    return dv.getInt32(arrPtr + 8, true);
  }

  function printUnion(ptr: number) {
    const dv = view();
    const tag = dv.getInt32(ptr, true);
    const data = ptr + 4;
    switch (tag) {
      case 0:
        return String(dv.getInt8(data));
      case 1:
        return String(dv.getInt16(data, true));
      case 2:
        return String(dv.getInt32(data, true));
      case 3:
        return String(dv.getBigInt64(data, true));
      case 4:
      case 5:
        return "<i128>";
      case 6:
        return String(dv.getUint8(data));
      case 7:
        return String(dv.getUint16(data, true));
      case 8:
        return String(dv.getUint32(data, true));
      case 9:
        return String(dv.getBigUint64(data, true));
      case 10:
      case 11:
        return "<u128>";
      case 12:
        return String(dv.getFloat32(data, true));
      case 13:
        return String(dv.getFloat64(data, true));
      case 14:
      case 15:
        return "<f128>";
      case 16: {
        const strPtr = dv.getUint32(data, true);
        return readCString(strPtr);
      }
      case 17: {
        const ch = dv.getUint8(data);
        return String.fromCharCode(ch);
      }
      case 18:
        return dv.getUint8(data) ? "true" : "false";
      default:
        return "<unknown>";
    }
  }

  function ferret_std_io_Print(slicePtr: number) {
    if (!slicePtr) return;
    const dv = view();
    const dataPtr = dv.getUint32(slicePtr + 0, true);
    const length = dv.getInt32(slicePtr + 4, true);
    const elemSize = dv.getUint32(slicePtr + 12, true);
    const parts: string[] = [];
    for (let i = 0; i < length; i++) {
      parts.push(printUnion(dataPtr + i * elemSize));
    }
    const text = parts.join(" ");
    emit(text);
    if (emitEvent) {
      emitEvent({ type: "output", text });
    }
  }

  function ferret_std_io_Println(slicePtr: number) {
    if (!slicePtr) {
      emit("");
      if (emitEvent) {
        emitEvent({ type: "output", text: "\n" });
      }
      return;
    }
    const dv = view();
    const dataPtr = dv.getUint32(slicePtr + 0, true);
    const length = dv.getInt32(slicePtr + 4, true);
    const elemSize = dv.getUint32(slicePtr + 12, true);
    const parts: string[] = [];
    for (let i = 0; i < length; i++) {
      parts.push(printUnion(dataPtr + i * elemSize));
    }
    const text = parts.join(" ") + "\n";
    emit(text);
    if (emitEvent) {
      emitEvent({ type: "output", text });
    }
  }

  function ferret_std_io_Read() {
    const line = nextInputLine();
    if (line == null) {
      return resultStrStr(null, "no input");
    }
    if (emitEvent) {
      emitEvent({ type: "input", text: line });
    }
    return resultStrStr(line, null);
  }

  function ferret_std_io_ReadUnsafe() {
    const line = nextInputLine();
    if (line == null) {
      if (throwOnInputNeeded) {
        const err = new Error("input needed");
        (err as any).code = "FERRET_INPUT";
        throw err;
      }
      return writeCString("");
    }
    if (emitEvent) {
      emitEvent({ type: "input", text: line });
    }
    return writeCString(line);
  }

  function ferret_std_io_ReadInt() {
    const line = nextInputLine();
    if (line == null) {
      return resultStrI32(null, "no input");
    }
    if (emitEvent) {
      emitEvent({ type: "input", text: line });
    }
    const trimmed = line.trim();
    if (!trimmed) {
      return resultStrI32(null, "invalid integer format");
    }
    const value = Number(trimmed);
    if (!Number.isFinite(value) || !Number.isInteger(value)) {
      return resultStrI32(null, "invalid integer format");
    }
    if (value < -2147483648 || value > 2147483647) {
      return resultStrI32(null, "integer out of range");
    }
    return resultStrI32(value, null);
  }

  function ferret_std_io_ReadFloat() {
    const line = nextInputLine();
    if (line == null) {
      return resultStrF64(null, "no input");
    }
    if (emitEvent) {
      emitEvent({ type: "input", text: line });
    }
    const trimmed = line.trim();
    if (!trimmed) {
      return resultStrF64(null, "invalid float format");
    }
    const value = Number(trimmed);
    if (!Number.isFinite(value)) {
      return resultStrF64(null, "invalid float format");
    }
    return resultStrF64(value, null);
  }

  function ferret_global_panic(msgPtr: number) {
    const msg = msgPtr ? readCString(msgPtr) : "panic";
    throw new Error(msg);
  }

  function ferret_string_len(ptr: number) {
    const mem = new Uint8Array(memory!.buffer);
    let i = ptr >>> 0;
    let len = 0;
    while (i < mem.length && mem[i] !== 0) {
      len++;
      i++;
    }
    return len;
  }

  function formatI64(value: number | bigint): string {
    if (typeof value === "bigint") {
      return BigInt.asIntN(64, value).toString();
    }
    return Math.trunc(value).toString();
  }

  function formatU64(value: number | bigint): string {
    const asBig = typeof value === "bigint" ? value : BigInt(Math.trunc(value));
    return BigInt.asUintN(64, asBig).toString();
  }

  function formatF64(value: number): string {
    let text = Number(value).toPrecision(15);
    const expIndex = text.search(/[eE]/);
    if (expIndex >= 0) {
      const head = text.slice(0, expIndex);
      const tail = text.slice(expIndex);
      const trimmed = head.includes(".") ? head.replace(/\.?0+$/, "") : head;
      text =
        (trimmed === "" || trimmed === "-" ? trimmed + "0" : trimmed) + tail;
    } else if (text.includes(".")) {
      const trimmed = text.replace(/\.?0+$/, "");
      text = trimmed === "" || trimmed === "-" ? trimmed + "0" : trimmed;
    }
    if (!text.includes(".") && expIndex < 0) {
      text += ".0";
    }
    return text;
  }

  function ferret_io_ConcatStrings(leftPtr: number, rightPtr: number) {
    const left = leftPtr ? readCString(leftPtr) : "";
    const right = rightPtr ? readCString(rightPtr) : "";
    return writeCString(left + right);
  }

  function ferret_string_concat_i64(strPtr: number, value: number | bigint) {
    const base = strPtr ? readCString(strPtr) : "";
    return writeCString(base + formatI64(value));
  }

  function ferret_string_concat_u64(strPtr: number, value: number | bigint) {
    const base = strPtr ? readCString(strPtr) : "";
    return writeCString(base + formatU64(value));
  }

  function ferret_string_concat_f64(strPtr: number, value: number) {
    const base = strPtr ? readCString(strPtr) : "";
    return writeCString(base + formatF64(value));
  }

  function ferret_string_concat_byte(strPtr: number, value: number) {
    const base = strPtr ? readCString(strPtr) : "";
    const ch = String.fromCharCode(Number(value) & 0xff);
    return writeCString(base + ch);
  }

  function ferret_string_concat_bool(strPtr: number, value: number) {
    const base = strPtr ? readCString(strPtr) : "";
    return writeCString(base + (value ? "true" : "false"));
  }

  function ferret_pow(base: number, exp: number) {
    return Math.pow(Number(base), Number(exp));
  }

  type FerretMapKey = number | bigint | string;
  type FerretMapEntry = { keyPtr: number; valuePtr: number };
  type FerretMapMeta = {
    keySize: number;
    valueSize: number;
    keyKind: "i32" | "i64" | "str" | "bytes";
    entries: Map<FerretMapKey, FerretMapEntry>;
  };

  // JS-side map storage: mapPtr -> metadata
  const ferretMapStore = new Map<number, FerretMapMeta>();
  const ferretMapIterStore = new Map<
    number,
    { entries: FerretMapEntry[]; index: number }
  >();

  function mapKeyRepr(meta: FerretMapMeta, keyPtr: number): FerretMapKey {
    const dv = view();
    switch (meta.keyKind) {
      case "i32":
        return dv.getInt32(keyPtr, true);
      case "i64":
        return dv.getBigInt64(keyPtr, true);
      case "str": {
        const strPtr = dv.getUint32(keyPtr, true);
        return readCString(strPtr);
      }
      case "bytes": {
        const mem = new Uint8Array(memory!.buffer, keyPtr >>> 0, meta.keySize);
        let out = "";
        for (let i = 0; i < mem.length; i++) {
          out += mem[i].toString(16).padStart(2, "0");
        }
        return out;
      }
    }
  }

  function mapGetMeta(mapPtr: number): FerretMapMeta | null {
    return ferretMapStore.get(mapPtr) ?? null;
  }

  function mapSetEntry(meta: FerretMapMeta, keyPtr: number, valuePtr: number) {
    const key = mapKeyRepr(meta, keyPtr);
    const existing = meta.entries.get(key);
    if (existing) {
      if (meta.valueSize > 0) {
        ferret_memcpy(existing.valuePtr, valuePtr, meta.valueSize);
      }
      return;
    }
    const keyStorage = meta.keySize > 0 ? ferret_alloc(meta.keySize) : 0;
    if (meta.keySize > 0) {
      ferret_memcpy(keyStorage, keyPtr, meta.keySize);
    }
    const valueStorage = meta.valueSize > 0 ? ferret_alloc(meta.valueSize) : 0;
    if (meta.valueSize > 0) {
      ferret_memcpy(valueStorage, valuePtr, meta.valueSize);
    }
    meta.entries.set(key, { keyPtr: keyStorage, valuePtr: valueStorage });
  }

  function mapFromPairs(
    keyKind: FerretMapMeta["keyKind"],
    keySize: number,
    valueSize: number,
    keysPtr: number,
    valuesPtr: number,
    count: number,
  ): number {
    const mapPtr = ferret_alloc(8);
    const meta: FerretMapMeta = {
      keySize: Number(keySize),
      valueSize: Number(valueSize),
      keyKind,
      entries: new Map(),
    };
    ferretMapStore.set(mapPtr, meta);
    const kSize = Number(keySize);
    const vSize = Number(valueSize);
    const total = Number(count);
    for (let i = 0; i < total; i++) {
      const kPtr = (keysPtr + i * kSize) >>> 0;
      const vPtr = (valuesPtr + i * vSize) >>> 0;
      mapSetEntry(meta, kPtr, vPtr);
    }
    return mapPtr >>> 0;
  }

  function ferret_map_new_i32(keySize: number, valueSize: number): number {
    const mapPtr = ferret_alloc(8);
    ferretMapStore.set(mapPtr, {
      keySize: Number(keySize),
      valueSize: Number(valueSize),
      keyKind: "i32",
      entries: new Map(),
    });
    return mapPtr >>> 0;
  }

  function ferret_map_new_i64(keySize: number, valueSize: number): number {
    const mapPtr = ferret_alloc(8);
    ferretMapStore.set(mapPtr, {
      keySize: Number(keySize),
      valueSize: Number(valueSize),
      keyKind: "i64",
      entries: new Map(),
    });
    return mapPtr >>> 0;
  }

  function ferret_map_new_str(keySize: number, valueSize: number): number {
    const mapPtr = ferret_alloc(8);
    ferretMapStore.set(mapPtr, {
      keySize: Number(keySize),
      valueSize: Number(valueSize),
      keyKind: "str",
      entries: new Map(),
    });
    return mapPtr >>> 0;
  }

  function ferret_map_new_bytes(keySize: number, valueSize: number): number {
    const mapPtr = ferret_alloc(8);
    ferretMapStore.set(mapPtr, {
      keySize: Number(keySize),
      valueSize: Number(valueSize),
      keyKind: "bytes",
      entries: new Map(),
    });
    return mapPtr >>> 0;
  }

  function ferret_map_from_pairs_i32(
    keySize: number,
    valueSize: number,
    keysPtr: number,
    valuesPtr: number,
    count: number,
  ): number {
    return mapFromPairs("i32", keySize, valueSize, keysPtr, valuesPtr, count);
  }

  function ferret_map_from_pairs_i64(
    keySize: number,
    valueSize: number,
    keysPtr: number,
    valuesPtr: number,
    count: number,
  ): number {
    return mapFromPairs("i64", keySize, valueSize, keysPtr, valuesPtr, count);
  }

  function ferret_map_from_pairs_str(
    keySize: number,
    valueSize: number,
    keysPtr: number,
    valuesPtr: number,
    count: number,
  ): number {
    return mapFromPairs("str", keySize, valueSize, keysPtr, valuesPtr, count);
  }

  function ferret_map_from_pairs_bytes(
    keySize: number,
    valueSize: number,
    keysPtr: number,
    valuesPtr: number,
    count: number,
  ): number {
    return mapFromPairs("bytes", keySize, valueSize, keysPtr, valuesPtr, count);
  }

  function ferret_map_get(mapPtr: number, keyPtr: number): number {
    const meta = mapGetMeta(mapPtr);
    if (!meta) {
      return 0;
    }
    const key = mapKeyRepr(meta, keyPtr);
    const entry = meta.entries.get(key);
    return entry ? entry.valuePtr >>> 0 : 0;
  }

  function ferret_map_get_optional_out(
    mapPtr: number,
    keyPtr: number,
    outPtr: number,
  ): void {
    if (!outPtr) {
      return;
    }
    const meta = mapGetMeta(mapPtr);
    const dv = view();
    const valueSize = meta ? meta.valueSize : 0;
    const flagPtr = outPtr + valueSize;
    if (!meta) {
      dv.setUint8(flagPtr, 0);
      return;
    }
    const key = mapKeyRepr(meta, keyPtr);
    const entry = meta.entries.get(key);
    if (entry) {
      if (valueSize > 0) {
        ferret_memcpy(outPtr, entry.valuePtr, valueSize);
      }
      dv.setUint8(flagPtr, 1);
    } else {
      dv.setUint8(flagPtr, 0);
    }
  }

  function ferret_map_set(
    mapPtr: number,
    keyPtr: number,
    valuePtr: number,
  ): void {
    const meta = mapGetMeta(mapPtr);
    if (!meta) {
      return;
    }
    mapSetEntry(meta, keyPtr, valuePtr);
  }

  function ferret_map_size(mapPtr: number): number {
    const meta = mapGetMeta(mapPtr);
    return meta ? meta.entries.size : 0;
  }

  function ferret_map_iter_begin(mapPtr: number, iterPtr: number): number {
    const meta = mapGetMeta(mapPtr);
    if (!meta || meta.entries.size === 0 || !iterPtr) {
      return 0;
    }
    const entries = Array.from(meta.entries.values());
    ferretMapIterStore.set(iterPtr, { entries, index: 0 });
    const dv = view();
    dv.setUint32(iterPtr, 0, true);
    dv.setUint32(iterPtr + 4, 1, true);
    return 1;
  }

  function ferret_map_iter_next(
    mapPtr: number,
    iterPtr: number,
    keyOutPtr: number,
    valueOutPtr: number,
  ): number {
    const iter = ferretMapIterStore.get(iterPtr);
    if (!iter || !iterPtr || !keyOutPtr || !valueOutPtr) {
      return 0;
    }
    if (iter.index >= iter.entries.length) {
      const dv = view();
      dv.setUint32(iterPtr + 4, 0, true);
      return 0;
    }
    const entry = iter.entries[iter.index];
    iter.index += 1;
    const dv = view();
    dv.setUint32(keyOutPtr, entry.keyPtr, true);
    dv.setUint32(valueOutPtr, entry.valuePtr, true);
    dv.setUint32(iterPtr, iter.index, true);
    dv.setUint32(iterPtr + 4, iter.index < iter.entries.length ? 1 : 0, true);
    return 1;
  }

  return {
    bind,
    imports: {
      ferret: {
        ferret_alloc,
        ferret_memcpy,
        ferret_optional_unwrap_or,
        ferret_array_new,
        ferret_array_clone,
        ferret_array_append,
        ferret_array_get,
        ferret_array_set,
        ferret_array_len,
        ferret_array_cap,
        ferret_std_io_Print,
        ferret_std_io_Println,
        ferret_std_io_Read,
        ferret_std_io_ReadUnsafe,
        ferret_std_io_ReadInt,
        ferret_std_io_ReadFloat,
        ferret_global_panic,
        ferret_string_len,
        ferret_io_ConcatStrings,
        ferret_string_concat_i64,
        ferret_string_concat_u64,
        ferret_string_concat_f64,
        ferret_string_concat_byte,
        ferret_string_concat_bool,
        ferret_pow,
        ferret_map_new_i32,
        ferret_map_new_i64,
        ferret_map_new_str,
        ferret_map_new_bytes,
        ferret_map_from_pairs_i32,
        ferret_map_from_pairs_i64,
        ferret_map_from_pairs_str,
        ferret_map_from_pairs_bytes,
        ferret_map_get,
        ferret_map_get_optional_out,
        ferret_map_set,
        ferret_map_size,
        ferret_map_iter_begin,
        ferret_map_iter_next,
      },
    },
  };
}
