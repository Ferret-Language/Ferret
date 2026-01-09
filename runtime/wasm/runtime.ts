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
        return readBigIntSigned(data, 16).toString();
      case 5:
        return readBigIntSigned(data, 32).toString();
      case 6:
        return String(dv.getUint8(data));
      case 7:
        return String(dv.getUint16(data, true));
      case 8:
        return String(dv.getUint32(data, true));
      case 9:
        return String(dv.getBigUint64(data, true));
      case 10:
        return readBigIntUnsigned(data, 16).toString();
      case 11:
        return readBigIntUnsigned(data, 32).toString();
      case 12:
        return String(dv.getFloat32(data, true));
      case 13:
        return String(dv.getFloat64(data, true));
      case 14:
        return softToString(
          readF128Bits(data),
          F128_FRAC_BITS,
          F128_EXP_BITS,
          F128_EXP_BIAS,
          F128_EXP_MAX,
          F128_MIN_EXP,
          F128_MAX_EXP,
          F128_DECIMAL_DIG,
        );
      case 15:
        return softToString(
          readF256Bits(data),
          F256_FRAC_BITS,
          F256_EXP_BITS,
          F256_EXP_BIAS,
          F256_EXP_MAX,
          F256_MIN_EXP,
          F256_MAX_EXP,
          F256_DECIMAL_DIG,
        );
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

  function formatFloat(value: number, precision: number): string {
    let text = Number(value).toPrecision(precision);
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

  function formatF64(value: number): string {
    return formatFloat(value, 15);
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

  const BIGINT_BITS_128 = 128;
  const BIGINT_BITS_256 = 256;
  const BIGINT_MASK_128 = (1n << 128n) - 1n;
  const BIGINT_MASK_256 = (1n << 256n) - 1n;
  const BIGINT_SIGN_128 = 1n << 127n;
  const BIGINT_SIGN_256 = 1n << 255n;

  function bigintMask(bits: number): bigint {
    return bits === BIGINT_BITS_128 ? BIGINT_MASK_128 : BIGINT_MASK_256;
  }

  function bigintSign(bits: number): bigint {
    return bits === BIGINT_BITS_128 ? BIGINT_SIGN_128 : BIGINT_SIGN_256;
  }

  function readBigIntUnsigned(ptr: number, byteLen: number): bigint {
    if (!ptr || byteLen <= 0) {
      return 0n;
    }
    const mem = new Uint8Array(memory!.buffer, ptr >>> 0, byteLen);
    let value = 0n;
    for (let i = mem.length - 1; i >= 0; i--) {
      value = (value << 8n) | BigInt(mem[i]);
    }
    return value;
  }

  function readBigIntSigned(ptr: number, byteLen: number): bigint {
    const unsigned = readBigIntUnsigned(ptr, byteLen);
    const bits = BigInt(byteLen * 8);
    const sign = 1n << (bits - 1n);
    if (unsigned & sign) {
      return unsigned - (1n << bits);
    }
    return unsigned;
  }

  function readBigIntUnsignedBits(ptr: number, bits: number): bigint {
    return readBigIntUnsigned(ptr, bits >> 3);
  }

  function readBigIntSignedBits(ptr: number, bits: number): bigint {
    return readBigIntSigned(ptr, bits >> 3);
  }

  function writeBigIntRaw(ptr: number, byteLen: number, value: bigint): void {
    if (!ptr || byteLen <= 0) {
      return;
    }
    const mem = new Uint8Array(memory!.buffer);
    let v = value;
    const base = ptr >>> 0;
    for (let i = 0; i < byteLen; i++) {
      mem[base + i] = Number(v & 0xffn);
      v >>= 8n;
    }
  }

  function wrapUnsigned(value: bigint, bits: number): bigint {
    return value & bigintMask(bits);
  }

  function wrapSigned(value: bigint, bits: number): bigint {
    const mask = bigintMask(bits);
    let v = value & mask;
    const sign = bigintSign(bits);
    if (v & sign) {
      v -= mask + 1n;
    }
    return v;
  }

  function writeBigIntUnsigned(ptr: number, bits: number, value: bigint): void {
    writeBigIntRaw(ptr, bits >> 3, wrapUnsigned(value, bits));
  }

  function writeBigIntSigned(ptr: number, bits: number, value: bigint): void {
    writeBigIntRaw(ptr, bits >> 3, wrapUnsigned(value, bits));
  }

  function bytesEqual(ptrA: number, ptrB: number, size: number): boolean {
    if (ptrA === ptrB) {
      return true;
    }
    if (!ptrA || !ptrB || size <= 0) {
      return false;
    }
    const mem = new Uint8Array(memory!.buffer);
    const a = ptrA >>> 0;
    const b = ptrB >>> 0;
    for (let i = 0; i < size; i++) {
      if (mem[a + i] !== mem[b + i]) {
        return false;
      }
    }
    return true;
  }

  const SOFT_EXTRA_BITS = 3n;

  const F128_FRAC_BITS = 112n;
  const F128_EXP_BITS = 15n;
  const F128_EXP_BIAS = 16383;
  const F128_EXP_MAX = (1n << F128_EXP_BITS) - 1n;
  const F128_SIG_BITS = F128_FRAC_BITS + 1n;
  const F128_MAX_EXP = 16383;
  const F128_MIN_EXP = -16382;
  const F128_DECIMAL_DIG = 36;

  const F256_FRAC_BITS = 236n;
  const F256_EXP_BITS = 19n;
  const F256_EXP_BIAS = 262143;
  const F256_EXP_MAX = (1n << F256_EXP_BITS) - 1n;
  const F256_SIG_BITS = F256_FRAC_BITS + 1n;
  const F256_MAX_EXP = 262143;
  const F256_MIN_EXP = -262142;
  const F256_DECIMAL_DIG = 73;

  const SOFT_CLASS_ZERO = 0;
  const SOFT_CLASS_NORMAL = 1;
  const SOFT_CLASS_INF = 2;
  const SOFT_CLASS_NAN = 3;

  const f64Buf = new ArrayBuffer(8);
  const f64View = new DataView(f64Buf);

  function readF128Bits(ptr: number): bigint {
    return readBigIntUnsigned(ptr, 16);
  }

  function writeF128Bits(ptr: number, bits: bigint): void {
    writeBigIntUnsigned(ptr, 128, bits);
  }

  function readF256Bits(ptr: number): bigint {
    return readBigIntUnsigned(ptr, 32);
  }

  function writeF256Bits(ptr: number, bits: bigint): void {
    writeBigIntUnsigned(ptr, 256, bits);
  }

  function bitLengthBigInt(value: bigint): number {
    if (value === 0n) {
      return 0;
    }
    return value.toString(2).length;
  }

  function log2FromBigInt(value: bigint): number {
    if (value === 0n) {
      return -Infinity;
    }
    const msb = bitLengthBigInt(value) - 1;
    let top: bigint;
    if (msb >= 63) {
      top = value >> BigInt(msb - 63);
    } else {
      top = value << BigInt(63 - msb);
    }
    const frac = Number(top) / Math.pow(2, 63);
    return msb + Math.log2(frac);
  }

  function pow5BigInt(exp: number): bigint {
    let result = 1n;
    let base = 5n;
    let e = exp;
    while (e > 0) {
      if (e % 2 === 1) {
        result *= base;
      }
      base *= base;
      e = Math.floor(e / 2);
    }
    return result;
  }

  function shiftRightSticky(sig: bigint, shift: number): bigint {
    if (shift <= 0) {
      return sig;
    }
    const sigBits = bitLengthBigInt(sig);
    if (shift >= sigBits) {
      return sig === 0n ? 0n : 1n;
    }
    const s = BigInt(shift);
    const lost = sig & ((1n << s) - 1n);
    sig >>= s;
    if (lost !== 0n) {
      sig |= 1n;
    }
    return sig;
  }

  function softUnpack(
    bits: bigint,
    fracBits: bigint,
    expBits: bigint,
    expBias: number,
    expMax: bigint,
    minExp: number,
  ) {
    const totalBits = fracBits + expBits + 1n;
    const sign = Number(bits >> (totalBits - 1n));
    const expMask = expMax;
    const fracMask = (1n << fracBits) - 1n;
    const expRaw = (bits >> fracBits) & expMask;
    const frac = bits & fracMask;
    if (expRaw === expMax) {
      return {
        sign,
        exp: 0,
        sig: frac,
        cls: frac === 0n ? SOFT_CLASS_INF : SOFT_CLASS_NAN,
      };
    }
    if (expRaw === 0n) {
      if (frac === 0n) {
        return { sign, exp: 0, sig: 0n, cls: SOFT_CLASS_ZERO };
      }
      return {
        sign,
        exp: minExp,
        sig: frac << SOFT_EXTRA_BITS,
        cls: SOFT_CLASS_NORMAL,
      };
    }
    const exp = Number(expRaw) - expBias;
    const sig = (frac | (1n << fracBits)) << SOFT_EXTRA_BITS;
    return { sign, exp, sig, cls: SOFT_CLASS_NORMAL };
  }

  function softPack(
    sign: number,
    exp: number,
    sig: bigint,
    cls: number,
    fracBits: bigint,
    expBits: bigint,
    expBias: number,
    expMax: bigint,
    minExp: number,
    maxExp: number,
  ): bigint {
    const totalBits = fracBits + expBits + 1n;
    if (cls === SOFT_CLASS_NAN) {
      return (BigInt(sign) << (totalBits - 1n)) | (expMax << fracBits) | 1n;
    }
    if (cls === SOFT_CLASS_INF) {
      return (BigInt(sign) << (totalBits - 1n)) | (expMax << fracBits);
    }
    if (cls === SOFT_CLASS_ZERO || sig === 0n) {
      return BigInt(sign) << (totalBits - 1n);
    }

    const sigBits = Number(fracBits + 1n);
    const target = sigBits - 1 + Number(SOFT_EXTRA_BITS);
    let lead = bitLengthBigInt(sig) - 1;
    if (lead > target) {
      sig = shiftRightSticky(sig, lead - target);
      exp += lead - target;
    } else if (lead < target) {
      sig <<= BigInt(target - lead);
      exp -= target - lead;
    }

    if (exp < minExp) {
      sig = shiftRightSticky(sig, minExp - exp);
      exp = minExp;
    }

    const guard = (sig >> 2n) & 1n;
    const round = (sig >> 1n) & 1n;
    const sticky = sig & 1n;
    sig >>= SOFT_EXTRA_BITS;
    if (guard && (round || sticky || sig & 1n)) {
      sig += 1n;
      if (sig >> BigInt(sigBits)) {
        sig >>= 1n;
        exp += 1;
      }
    }

    if (sig === 0n) {
      return BigInt(sign) << (totalBits - 1n);
    }
    if (exp > maxExp) {
      return (BigInt(sign) << (totalBits - 1n)) | (expMax << fracBits);
    }

    const hiddenBit = 1n << BigInt(sigBits - 1);
    let normal = exp > minExp;
    if (exp === minExp) {
      normal = (sig & hiddenBit) !== 0n;
    }
    const fracMask = (1n << fracBits) - 1n;
    const frac = normal ? sig & (hiddenBit - 1n) : sig & fracMask;
    const expField = normal ? BigInt(exp + expBias) : 0n;
    return (BigInt(sign) << (totalBits - 1n)) | (expField << fracBits) | frac;
  }

  function softCompare(
    aBits: bigint,
    bBits: bigint,
    fracBits: bigint,
    expBits: bigint,
    expBias: number,
    expMax: bigint,
    minExp: number,
    maxExp: number,
  ) {
    const a = softUnpack(aBits, fracBits, expBits, expBias, expMax, minExp);
    const b = softUnpack(bBits, fracBits, expBits, expBias, expMax, minExp);
    if (a.cls === SOFT_CLASS_NAN || b.cls === SOFT_CLASS_NAN) {
      return { unordered: true, cmp: 0 };
    }
    if (a.cls === SOFT_CLASS_ZERO && b.cls === SOFT_CLASS_ZERO) {
      return { unordered: false, cmp: 0 };
    }
    if (a.cls === SOFT_CLASS_INF || b.cls === SOFT_CLASS_INF) {
      if (a.cls === b.cls) {
        if (a.sign === b.sign) {
          return { unordered: false, cmp: 0 };
        }
        return { unordered: false, cmp: a.sign ? -1 : 1 };
      }
      if (a.cls === SOFT_CLASS_INF) {
        return { unordered: false, cmp: a.sign ? -1 : 1 };
      }
      return { unordered: false, cmp: b.sign ? 1 : -1 };
    }
    if (a.sign !== b.sign) {
      return { unordered: false, cmp: a.sign ? -1 : 1 };
    }
    const sigA = a.sig >> SOFT_EXTRA_BITS;
    const sigB = b.sig >> SOFT_EXTRA_BITS;
    let cmp = 0;
    if (a.exp < b.exp) {
      cmp = -1;
    } else if (a.exp > b.exp) {
      cmp = 1;
    } else if (sigA < sigB) {
      cmp = -1;
    } else if (sigA > sigB) {
      cmp = 1;
    }
    return { unordered: false, cmp: a.sign ? -cmp : cmp };
  }

  function softAdd(
    aBits: bigint,
    bBits: bigint,
    sub: boolean,
    fracBits: bigint,
    expBits: bigint,
    expBias: number,
    expMax: bigint,
    minExp: number,
    maxExp: number,
  ): bigint {
    let a = softUnpack(aBits, fracBits, expBits, expBias, expMax, minExp);
    let b = softUnpack(bBits, fracBits, expBits, expBias, expMax, minExp);
    if (sub) {
      b = { ...b, sign: b.sign ^ 1 };
    }
    if (a.cls === SOFT_CLASS_NAN || b.cls === SOFT_CLASS_NAN) {
      return softPack(
        0,
        0,
        0n,
        SOFT_CLASS_NAN,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (a.cls === SOFT_CLASS_INF || b.cls === SOFT_CLASS_INF) {
      if (
        a.cls === SOFT_CLASS_INF &&
        b.cls === SOFT_CLASS_INF &&
        a.sign !== b.sign
      ) {
        return softPack(
          0,
          0,
          0n,
          SOFT_CLASS_NAN,
          fracBits,
          expBits,
          expBias,
          expMax,
          minExp,
          maxExp,
        );
      }
      if (a.cls === SOFT_CLASS_INF) {
        return softPack(
          a.sign,
          0,
          0n,
          SOFT_CLASS_INF,
          fracBits,
          expBits,
          expBias,
          expMax,
          minExp,
          maxExp,
        );
      }
      return softPack(
        b.sign,
        0,
        0n,
        SOFT_CLASS_INF,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (a.cls === SOFT_CLASS_ZERO) {
      return softPack(
        b.sign,
        b.exp,
        b.sig,
        b.cls,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (b.cls === SOFT_CLASS_ZERO) {
      return softPack(
        a.sign,
        a.exp,
        a.sig,
        a.cls,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }

    if (a.exp < b.exp) {
      const temp = a;
      a = b;
      b = temp;
    }
    const diff = a.exp - b.exp;
    if (diff > 0) {
      b = { ...b, sig: shiftRightSticky(b.sig, diff) };
    }
    if (a.sign === b.sign) {
      const sig = a.sig + b.sig;
      return softPack(
        a.sign,
        a.exp,
        sig,
        SOFT_CLASS_NORMAL,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (a.sig === b.sig) {
      return softPack(
        0,
        0,
        0n,
        SOFT_CLASS_ZERO,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (a.sig < b.sig) {
      const sig = b.sig - a.sig;
      return softPack(
        b.sign,
        a.exp,
        sig,
        SOFT_CLASS_NORMAL,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    const sig = a.sig - b.sig;
    return softPack(
      a.sign,
      a.exp,
      sig,
      SOFT_CLASS_NORMAL,
      fracBits,
      expBits,
      expBias,
      expMax,
      minExp,
      maxExp,
    );
  }

  function softMul(
    aBits: bigint,
    bBits: bigint,
    fracBits: bigint,
    expBits: bigint,
    expBias: number,
    expMax: bigint,
    minExp: number,
    maxExp: number,
  ): bigint {
    const a = softUnpack(aBits, fracBits, expBits, expBias, expMax, minExp);
    const b = softUnpack(bBits, fracBits, expBits, expBias, expMax, minExp);
    if (a.cls === SOFT_CLASS_NAN || b.cls === SOFT_CLASS_NAN) {
      return softPack(
        0,
        0,
        0n,
        SOFT_CLASS_NAN,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (
      (a.cls === SOFT_CLASS_INF && b.cls === SOFT_CLASS_ZERO) ||
      (b.cls === SOFT_CLASS_INF && a.cls === SOFT_CLASS_ZERO)
    ) {
      return softPack(
        0,
        0,
        0n,
        SOFT_CLASS_NAN,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (a.cls === SOFT_CLASS_INF || b.cls === SOFT_CLASS_INF) {
      return softPack(
        a.sign ^ b.sign,
        0,
        0n,
        SOFT_CLASS_INF,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (a.cls === SOFT_CLASS_ZERO || b.cls === SOFT_CLASS_ZERO) {
      return softPack(
        a.sign ^ b.sign,
        0,
        0n,
        SOFT_CLASS_ZERO,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    const sigA = a.sig >> SOFT_EXTRA_BITS;
    const sigB = b.sig >> SOFT_EXTRA_BITS;
    const product = sigA * sigB;
    const sigBits = Number(fracBits + 1n);
    const lead = bitLengthBigInt(product) - 1;
    const shiftRaw = lead - (sigBits - 1);
    const shift = shiftRaw - Number(SOFT_EXTRA_BITS);
    let sig = product;
    if (shift >= 0) {
      sig = shiftRightSticky(sig, shift);
    } else {
      sig <<= BigInt(-shift);
    }
    const exp = a.exp + b.exp - (sigBits - 1) + shiftRaw;
    return softPack(
      a.sign ^ b.sign,
      exp,
      sig,
      SOFT_CLASS_NORMAL,
      fracBits,
      expBits,
      expBias,
      expMax,
      minExp,
      maxExp,
    );
  }

  function softDiv(
    aBits: bigint,
    bBits: bigint,
    fracBits: bigint,
    expBits: bigint,
    expBias: number,
    expMax: bigint,
    minExp: number,
    maxExp: number,
  ): bigint {
    const a = softUnpack(aBits, fracBits, expBits, expBias, expMax, minExp);
    const b = softUnpack(bBits, fracBits, expBits, expBias, expMax, minExp);
    if (a.cls === SOFT_CLASS_NAN || b.cls === SOFT_CLASS_NAN) {
      return softPack(
        0,
        0,
        0n,
        SOFT_CLASS_NAN,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (a.cls === SOFT_CLASS_INF && b.cls === SOFT_CLASS_INF) {
      return softPack(
        0,
        0,
        0n,
        SOFT_CLASS_NAN,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (a.cls === SOFT_CLASS_INF) {
      return softPack(
        a.sign ^ b.sign,
        0,
        0n,
        SOFT_CLASS_INF,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (b.cls === SOFT_CLASS_INF) {
      return softPack(
        a.sign ^ b.sign,
        0,
        0n,
        SOFT_CLASS_ZERO,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (b.cls === SOFT_CLASS_ZERO) {
      if (a.cls === SOFT_CLASS_ZERO) {
        return softPack(
          0,
          0,
          0n,
          SOFT_CLASS_NAN,
          fracBits,
          expBits,
          expBias,
          expMax,
          minExp,
          maxExp,
        );
      }
      return softPack(
        a.sign ^ b.sign,
        0,
        0n,
        SOFT_CLASS_INF,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (a.cls === SOFT_CLASS_ZERO) {
      return softPack(
        a.sign ^ b.sign,
        0,
        0n,
        SOFT_CLASS_ZERO,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }

    const sigBits = Number(fracBits + 1n);
    const sigA = a.sig >> SOFT_EXTRA_BITS;
    const sigB = b.sig >> SOFT_EXTRA_BITS;
    const shift = sigBits - 1 + Number(SOFT_EXTRA_BITS);
    const numerator = sigA << BigInt(shift);
    let quotient = numerator / sigB;
    const remainder = numerator % sigB;
    if (remainder !== 0n) {
      quotient |= 1n;
    }
    const exp = a.exp - b.exp;
    return softPack(
      a.sign ^ b.sign,
      exp,
      quotient,
      SOFT_CLASS_NORMAL,
      fracBits,
      expBits,
      expBias,
      expMax,
      minExp,
      maxExp,
    );
  }

  function softToNumber(
    sign: number,
    exp: number,
    sig: bigint,
    sigBits: bigint,
  ): number {
    if (sig === 0n) {
      return sign ? -0 : 0;
    }
    const sigRaw = sig >> SOFT_EXTRA_BITS;
    const value = Number(sigRaw) * Math.pow(2, exp - (Number(sigBits) - 1));
    return sign ? -value : value;
  }

  function f64ToBits(value: number): bigint {
    f64View.setFloat64(0, value, true);
    return f64View.getBigUint64(0, true);
  }

  function f64ToSoftBits(
    value: number,
    fracBits: bigint,
    expBits: bigint,
    expBias: number,
    expMax: bigint,
    minExp: number,
    maxExp: number,
  ): bigint {
    const bits = f64ToBits(value);
    const sign = Number(bits >> 63n);
    const expRaw = (bits >> 52n) & 0x7ffn;
    const frac = bits & 0xfffffffffffffn;
    if (expRaw === 0x7ffn) {
      const cls = frac === 0n ? SOFT_CLASS_INF : SOFT_CLASS_NAN;
      return softPack(
        sign,
        0,
        0n,
        cls,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (expRaw === 0n) {
      if (frac === 0n) {
        return softPack(
          sign,
          0,
          0n,
          SOFT_CLASS_ZERO,
          fracBits,
          expBits,
          expBias,
          expMax,
          minExp,
          maxExp,
        );
      }
      const lead = bitLengthBigInt(frac) - 1;
      const shift = 52 - lead;
      let sig = frac << BigInt(shift);
      const exp = lead - 1074;
      sig <<= BigInt(Number(fracBits) - 52);
      sig <<= SOFT_EXTRA_BITS;
      return softPack(
        sign,
        exp,
        sig,
        SOFT_CLASS_NORMAL,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    const exp = Number(expRaw) - 1023;
    let sig = (1n << 52n) | frac;
    sig <<= BigInt(Number(fracBits) - 52);
    sig <<= SOFT_EXTRA_BITS;
    return softPack(
      sign,
      exp,
      sig,
      SOFT_CLASS_NORMAL,
      fracBits,
      expBits,
      expBias,
      expMax,
      minExp,
      maxExp,
    );
  }

  function softBitsToNumber(
    bits: bigint,
    fracBits: bigint,
    expBits: bigint,
    expBias: number,
    expMax: bigint,
    minExp: number,
  ): number {
    const unpacked = softUnpack(
      bits,
      fracBits,
      expBits,
      expBias,
      expMax,
      minExp,
    );
    if (unpacked.cls === SOFT_CLASS_NAN) {
      return NaN;
    }
    if (unpacked.cls === SOFT_CLASS_INF) {
      return unpacked.sign ? -Infinity : Infinity;
    }
    return softToNumber(
      unpacked.sign,
      unpacked.exp,
      unpacked.sig,
      fracBits + 1n,
    );
  }

  type SoftParsedFloat = {
    sign: number;
    digits: string | null;
    exp10: number;
    cls: number;
  };

  function parseSoftFloatString(text: string): SoftParsedFloat | null {
    if (!text) {
      return null;
    }
    let s = text.trim();
    if (!s) {
      return null;
    }
    s = s.replace(/_/g, "");
    let sign = 0;
    if (s[0] === "+" || s[0] === "-") {
      sign = s[0] === "-" ? 1 : 0;
      s = s.slice(1);
    }
    if (!s) {
      return null;
    }
    const lower = s.toLowerCase();
    if (lower.startsWith("inf")) {
      return { sign, digits: null, exp10: 0, cls: SOFT_CLASS_INF };
    }
    if (lower.startsWith("nan")) {
      return { sign, digits: null, exp10: 0, cls: SOFT_CLASS_NAN };
    }
    let digits = "";
    let digitsBefore = 0;
    let seenDot = false;
    let i = 0;
    for (; i < s.length; i++) {
      const c = s[i];
      if (c >= "0" && c <= "9") {
        digits += c;
        if (!seenDot) {
          digitsBefore += 1;
        }
      } else if (c === "." && !seenDot) {
        seenDot = true;
      } else {
        break;
      }
    }
    if (digits.length === 0) {
      return null;
    }
    let exp10 = 0;
    if (i < s.length && (s[i] === "e" || s[i] === "E")) {
      i++;
      let expSign = 1;
      if (s[i] === "+" || s[i] === "-") {
        expSign = s[i] === "-" ? -1 : 1;
        i++;
      }
      let expVal = 0;
      let hasDigits = false;
      for (; i < s.length; i++) {
        const c = s[i];
        if (c >= "0" && c <= "9") {
          expVal = expVal * 10 + (c.charCodeAt(0) - 48);
          hasDigits = true;
        } else {
          break;
        }
      }
      if (hasDigits) {
        exp10 = expSign * expVal;
      }
    }
    const digitsAfter = digits.length - digitsBefore;
    exp10 -= digitsAfter;
    digits = digits.replace(/^0+/, "");
    if (!digits) {
      return { sign, digits: null, exp10: 0, cls: SOFT_CLASS_ZERO };
    }
    return { sign, digits, exp10, cls: SOFT_CLASS_NORMAL };
  }

  function softFormatDecimal(
    sign: number,
    exp: number,
    sigRaw: bigint,
    sigBits: number,
    precision: number,
  ): string {
    if (sigRaw === 0n) {
      return sign ? "-0.0" : "0.0";
    }
    const exp2 = exp - (sigBits - 1);
    const log10Val = (log2FromBigInt(sigRaw) + exp2) * Math.log10(2);
    let exp10 = Math.floor(log10Val);
    const k = precision - 1 - exp10;
    let num = sigRaw;
    let den = 1n;
    if (k >= 0) {
      num *= pow5BigInt(k);
    } else {
      den *= pow5BigInt(-k);
    }
    const shift2 = exp2 + k;
    if (shift2 >= 0) {
      num <<= BigInt(shift2);
    } else {
      den <<= BigInt(-shift2);
    }
    let q = num / den;
    const r = num % den;
    if (r !== 0n) {
      const twice = r * 2n;
      if (twice > den || (twice === den && (q & 1n) === 1n)) {
        q += 1n;
      }
    }
    let digits = q.toString();
    if (digits.length > precision) {
      q /= 10n;
      exp10 += 1;
      digits = q.toString();
    }
    if (digits.length < precision) {
      const diff = precision - digits.length;
      digits = "0".repeat(diff) + digits;
      exp10 -= diff;
    }
    const useFixed = exp10 >= -4 && exp10 < precision;
    let out = sign ? "-" : "";
    if (useFixed) {
      const point = exp10 + 1;
      if (point <= 0) {
        out += "0." + "0".repeat(-point) + digits;
      } else if (point >= digits.length) {
        out += digits + "0".repeat(point - digits.length) + ".0";
      } else {
        out += digits.slice(0, point) + "." + digits.slice(point);
      }
    } else {
      out += digits[0] + ".";
      if (digits.length > 1) {
        out += digits.slice(1);
      } else {
        out += "0";
      }
      out += "e";
      out += exp10 >= 0 ? "+" : "-";
      out += Math.abs(exp10).toString();
    }
    return out;
  }

  function softFromDecimalString(
    text: string,
    fracBits: bigint,
    expBits: bigint,
    expBias: number,
    expMax: bigint,
    minExp: number,
    maxExp: number,
  ): bigint {
    const parsed = parseSoftFloatString(text);
    if (!parsed) {
      return softPack(
        0,
        0,
        0n,
        SOFT_CLASS_ZERO,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (parsed.cls === SOFT_CLASS_NAN) {
      return softPack(
        parsed.sign,
        0,
        0n,
        SOFT_CLASS_NAN,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (parsed.cls === SOFT_CLASS_INF) {
      return softPack(
        parsed.sign,
        0,
        0n,
        SOFT_CLASS_INF,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (!parsed.digits) {
      return softPack(
        parsed.sign,
        0,
        0n,
        SOFT_CLASS_ZERO,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }

    const dec = BigInt(parsed.digits);
    const sigBits = Number(fracBits + 1n);
    const minSub = minExp - (sigBits - 1);
    const log2Val = log2FromBigInt(dec) + parsed.exp10 * Math.log2(10);
    const exp2 = Math.floor(log2Val);
    if (exp2 > maxExp) {
      return softPack(
        parsed.sign,
        0,
        0n,
        SOFT_CLASS_INF,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }
    if (exp2 < minSub) {
      return softPack(
        parsed.sign,
        0,
        0n,
        SOFT_CLASS_ZERO,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }

    let targetExp = exp2 < minExp ? minExp : exp2;
    const shift = sigBits - 1 - targetExp;
    const shift2 = parsed.exp10 + shift;

    let num = dec;
    let den = 1n;
    if (parsed.exp10 >= 0) {
      num *= pow5BigInt(parsed.exp10);
    } else {
      den *= pow5BigInt(-parsed.exp10);
    }
    if (shift2 >= 0) {
      num <<= BigInt(shift2);
    } else {
      den <<= BigInt(-shift2);
    }

    let q = num / den;
    const r = num % den;
    if (r !== 0n) {
      const twice = r * 2n;
      if (twice > den || (twice === den && (q & 1n) === 1n)) {
        q += 1n;
      }
    }

    const qBits = bitLengthBigInt(q);
    if (qBits > sigBits) {
      q >>= 1n;
      targetExp += 1;
    }
    if (targetExp > maxExp) {
      return softPack(
        parsed.sign,
        0,
        0n,
        SOFT_CLASS_INF,
        fracBits,
        expBits,
        expBias,
        expMax,
        minExp,
        maxExp,
      );
    }

    const sig = q << SOFT_EXTRA_BITS;
    return softPack(
      parsed.sign,
      targetExp,
      sig,
      SOFT_CLASS_NORMAL,
      fracBits,
      expBits,
      expBias,
      expMax,
      minExp,
      maxExp,
    );
  }

  function softToString(
    bits: bigint,
    fracBits: bigint,
    expBits: bigint,
    expBias: number,
    expMax: bigint,
    minExp: number,
    maxExp: number,
    precision: number,
  ): string {
    const unpacked = softUnpack(
      bits,
      fracBits,
      expBits,
      expBias,
      expMax,
      minExp,
    );
    if (unpacked.cls === SOFT_CLASS_NAN) {
      return "nan";
    }
    if (unpacked.cls === SOFT_CLASS_INF) {
      return unpacked.sign ? "-inf" : "inf";
    }
    if (unpacked.cls === SOFT_CLASS_ZERO) {
      return unpacked.sign ? "-0.0" : "0.0";
    }
    const sigRaw = unpacked.sig >> SOFT_EXTRA_BITS;
    return softFormatDecimal(
      unpacked.sign,
      unpacked.exp,
      sigRaw,
      Number(fracBits + 1n),
      precision,
    );
  }

  function parseBigIntLiteral(text: string, allowSign: boolean): bigint | null {
    if (!text) {
      return null;
    }
    let cleaned = text.trim();
    if (cleaned === "") {
      return null;
    }
    cleaned = cleaned.replace(/_/g, "");
    if (cleaned[0] === "+") {
      cleaned = cleaned.slice(1);
    }
    if (cleaned[0] === "-" && !allowSign) {
      return null;
    }
    try {
      return BigInt(cleaned);
    } catch {
      return null;
    }
  }

  function normalizeI64(value: number | bigint): bigint {
    return BigInt.asIntN(64, typeof value === "bigint" ? value : BigInt(value));
  }

  function normalizeU64(value: number | bigint): bigint {
    return BigInt.asUintN(
      64,
      typeof value === "bigint" ? value : BigInt(value),
    );
  }

  function powSigned(value: bigint, exp: bigint, bits: number): bigint {
    if (exp < 0n) {
      return 0n;
    }
    let result = 1n;
    let base = wrapSigned(value, bits);
    let e = exp;
    while (e > 0n) {
      if (e & 1n) {
        result = wrapSigned(result * base, bits);
      }
      base = wrapSigned(base * base, bits);
      e >>= 1n;
    }
    return wrapSigned(result, bits);
  }

  function powUnsigned(value: bigint, exp: bigint, bits: number): bigint {
    let result = 1n;
    let base = wrapUnsigned(value, bits);
    let e = exp;
    while (e > 0n) {
      if (e & 1n) {
        result = wrapUnsigned(result * base, bits);
      }
      base = wrapUnsigned(base * base, bits);
      e >>= 1n;
    }
    return wrapUnsigned(result, bits);
  }

  function ferret_i128_add_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntSignedBits(aPtr, BIGINT_BITS_128) +
      readBigIntSignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntSigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_i128_sub_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntSignedBits(aPtr, BIGINT_BITS_128) -
      readBigIntSignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntSigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_i128_mul_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntSignedBits(aPtr, BIGINT_BITS_128) *
      readBigIntSignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntSigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_i128_div_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntSignedBits(aPtr, BIGINT_BITS_128) /
      readBigIntSignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntSigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_i128_mod_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntSignedBits(aPtr, BIGINT_BITS_128) %
      readBigIntSignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntSigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_i128_eq_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntSignedBits(aPtr, BIGINT_BITS_128) ===
      readBigIntSignedBits(bPtr, BIGINT_BITS_128)
      ? 1
      : 0;
  }

  function ferret_i128_lt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntSignedBits(aPtr, BIGINT_BITS_128) <
      readBigIntSignedBits(bPtr, BIGINT_BITS_128)
      ? 1
      : 0;
  }

  function ferret_i128_gt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntSignedBits(aPtr, BIGINT_BITS_128) >
      readBigIntSignedBits(bPtr, BIGINT_BITS_128)
      ? 1
      : 0;
  }

  function ferret_i128_and_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const a = readBigIntSignedBits(aPtr, BIGINT_BITS_128);
    const b = readBigIntSignedBits(bPtr, BIGINT_BITS_128);
    const res = wrapSigned(
      a & BIGINT_MASK_128 & (b & BIGINT_MASK_128),
      BIGINT_BITS_128,
    );
    writeBigIntSigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_i128_or_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const a = readBigIntSignedBits(aPtr, BIGINT_BITS_128);
    const b = readBigIntSignedBits(bPtr, BIGINT_BITS_128);
    const res = wrapSigned(
      (a & BIGINT_MASK_128) | (b & BIGINT_MASK_128),
      BIGINT_BITS_128,
    );
    writeBigIntSigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_i128_xor_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const a = readBigIntSignedBits(aPtr, BIGINT_BITS_128);
    const b = readBigIntSignedBits(bPtr, BIGINT_BITS_128);
    const res = wrapSigned(
      (a & BIGINT_MASK_128) ^ (b & BIGINT_MASK_128),
      BIGINT_BITS_128,
    );
    writeBigIntSigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_i128_pow_ptr(
    basePtr: number,
    expPtr: number,
    outPtr: number,
  ) {
    if (!outPtr || !basePtr || !expPtr) return;
    const base = readBigIntSignedBits(basePtr, BIGINT_BITS_128);
    const exp = readBigIntSignedBits(expPtr, BIGINT_BITS_128);
    const res = powSigned(base, exp, BIGINT_BITS_128);
    writeBigIntSigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_u128_add_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) +
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_u128_sub_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) -
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_u128_mul_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) *
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_u128_div_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) /
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_u128_mod_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) %
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_u128_eq_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) ===
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128)
      ? 1
      : 0;
  }

  function ferret_u128_lt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) <
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128)
      ? 1
      : 0;
  }

  function ferret_u128_gt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) >
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128)
      ? 1
      : 0;
  }

  function ferret_u128_and_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) &
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_u128_or_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) |
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_u128_xor_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_128) ^
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_128);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_u128_pow_ptr(
    basePtr: number,
    expPtr: number,
    outPtr: number,
  ) {
    if (!outPtr || !basePtr || !expPtr) return;
    const base = readBigIntUnsignedBits(basePtr, BIGINT_BITS_128);
    const exp = readBigIntUnsignedBits(expPtr, BIGINT_BITS_128);
    const res = powUnsigned(base, exp, BIGINT_BITS_128);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, res);
  }

  function ferret_i256_add_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntSignedBits(aPtr, BIGINT_BITS_256) +
      readBigIntSignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntSigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_i256_sub_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntSignedBits(aPtr, BIGINT_BITS_256) -
      readBigIntSignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntSigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_i256_mul_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntSignedBits(aPtr, BIGINT_BITS_256) *
      readBigIntSignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntSigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_i256_div_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntSignedBits(aPtr, BIGINT_BITS_256) /
      readBigIntSignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntSigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_i256_mod_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntSignedBits(aPtr, BIGINT_BITS_256) %
      readBigIntSignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntSigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_i256_eq_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntSignedBits(aPtr, BIGINT_BITS_256) ===
      readBigIntSignedBits(bPtr, BIGINT_BITS_256)
      ? 1
      : 0;
  }

  function ferret_i256_lt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntSignedBits(aPtr, BIGINT_BITS_256) <
      readBigIntSignedBits(bPtr, BIGINT_BITS_256)
      ? 1
      : 0;
  }

  function ferret_i256_gt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntSignedBits(aPtr, BIGINT_BITS_256) >
      readBigIntSignedBits(bPtr, BIGINT_BITS_256)
      ? 1
      : 0;
  }

  function ferret_i256_and_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const a = readBigIntSignedBits(aPtr, BIGINT_BITS_256);
    const b = readBigIntSignedBits(bPtr, BIGINT_BITS_256);
    const res = wrapSigned(
      a & BIGINT_MASK_256 & (b & BIGINT_MASK_256),
      BIGINT_BITS_256,
    );
    writeBigIntSigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_i256_or_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const a = readBigIntSignedBits(aPtr, BIGINT_BITS_256);
    const b = readBigIntSignedBits(bPtr, BIGINT_BITS_256);
    const res = wrapSigned(
      (a & BIGINT_MASK_256) | (b & BIGINT_MASK_256),
      BIGINT_BITS_256,
    );
    writeBigIntSigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_i256_xor_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const a = readBigIntSignedBits(aPtr, BIGINT_BITS_256);
    const b = readBigIntSignedBits(bPtr, BIGINT_BITS_256);
    const res = wrapSigned(
      (a & BIGINT_MASK_256) ^ (b & BIGINT_MASK_256),
      BIGINT_BITS_256,
    );
    writeBigIntSigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_i256_pow_ptr(
    basePtr: number,
    expPtr: number,
    outPtr: number,
  ) {
    if (!outPtr || !basePtr || !expPtr) return;
    const base = readBigIntSignedBits(basePtr, BIGINT_BITS_256);
    const exp = readBigIntSignedBits(expPtr, BIGINT_BITS_256);
    const res = powSigned(base, exp, BIGINT_BITS_256);
    writeBigIntSigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_u256_add_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) +
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_u256_sub_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) -
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_u256_mul_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) *
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_u256_div_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) /
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_u256_mod_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) %
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_u256_eq_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) ===
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256)
      ? 1
      : 0;
  }

  function ferret_u256_lt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) <
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256)
      ? 1
      : 0;
  }

  function ferret_u256_gt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    return readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) >
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256)
      ? 1
      : 0;
  }

  function ferret_u256_and_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) &
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_u256_or_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) |
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_u256_xor_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const res =
      readBigIntUnsignedBits(aPtr, BIGINT_BITS_256) ^
      readBigIntUnsignedBits(bPtr, BIGINT_BITS_256);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_u256_pow_ptr(
    basePtr: number,
    expPtr: number,
    outPtr: number,
  ) {
    if (!outPtr || !basePtr || !expPtr) return;
    const base = readBigIntUnsignedBits(basePtr, BIGINT_BITS_256);
    const exp = readBigIntUnsignedBits(expPtr, BIGINT_BITS_256);
    const res = powUnsigned(base, exp, BIGINT_BITS_256);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, res);
  }

  function ferret_i128_from_i64_ptr(value: number | bigint, outPtr: number) {
    if (!outPtr) return;
    writeBigIntSigned(outPtr, BIGINT_BITS_128, normalizeI64(value));
  }

  function ferret_u128_from_u64_ptr(value: number | bigint, outPtr: number) {
    if (!outPtr) return;
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, normalizeU64(value));
  }

  function ferret_i256_from_i64_ptr(value: number | bigint, outPtr: number) {
    if (!outPtr) return;
    writeBigIntSigned(outPtr, BIGINT_BITS_256, normalizeI64(value));
  }

  function ferret_u256_from_u64_ptr(value: number | bigint, outPtr: number) {
    if (!outPtr) return;
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, normalizeU64(value));
  }

  function ferret_f128_from_f64_ptr(value: number, outPtr: number) {
    if (!outPtr) return;
    const bits = f64ToSoftBits(
      Number(value),
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
      F128_MAX_EXP,
    );
    writeF128Bits(outPtr, bits);
  }

  function ferret_f256_from_f64_ptr(value: number, outPtr: number) {
    if (!outPtr) return;
    const bits = f64ToSoftBits(
      Number(value),
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
      F256_MAX_EXP,
    );
    writeF256Bits(outPtr, bits);
  }

  function ferret_i128_to_i64_ptr(ptr: number): bigint {
    if (!ptr) return 0n;
    const unsigned = readBigIntUnsignedBits(ptr, BIGINT_BITS_128);
    return BigInt.asIntN(64, unsigned);
  }

  function ferret_u128_to_u64_ptr(ptr: number): bigint {
    if (!ptr) return 0n;
    const unsigned = readBigIntUnsignedBits(ptr, BIGINT_BITS_128);
    return BigInt.asUintN(64, unsigned);
  }

  function ferret_i256_to_i64_ptr(ptr: number): bigint {
    if (!ptr) return 0n;
    const unsigned = readBigIntUnsignedBits(ptr, BIGINT_BITS_256);
    return BigInt.asIntN(64, unsigned);
  }

  function ferret_u256_to_u64_ptr(ptr: number): bigint {
    if (!ptr) return 0n;
    const unsigned = readBigIntUnsignedBits(ptr, BIGINT_BITS_256);
    return BigInt.asUintN(64, unsigned);
  }

  function ferret_f128_to_f64_ptr(ptr: number): number {
    if (!ptr) return 0;
    return softBitsToNumber(
      readF128Bits(ptr),
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
    );
  }

  function ferret_f256_to_f64_ptr(ptr: number): number {
    if (!ptr) return 0;
    return softBitsToNumber(
      readF256Bits(ptr),
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
    );
  }

  function ferret_i128_to_string_ptr(ptr: number): number {
    if (!ptr) return writeCString("0");
    const value = readBigIntSignedBits(ptr, BIGINT_BITS_128);
    return writeCString(value.toString());
  }

  function ferret_u128_to_string_ptr(ptr: number): number {
    if (!ptr) return writeCString("0");
    const value = readBigIntUnsignedBits(ptr, BIGINT_BITS_128);
    return writeCString(value.toString());
  }

  function ferret_i256_to_string_ptr(ptr: number): number {
    if (!ptr) return writeCString("0");
    const value = readBigIntSignedBits(ptr, BIGINT_BITS_256);
    return writeCString(value.toString());
  }

  function ferret_u256_to_string_ptr(ptr: number): number {
    if (!ptr) return writeCString("0");
    const value = readBigIntUnsignedBits(ptr, BIGINT_BITS_256);
    return writeCString(value.toString());
  }

  function ferret_f128_to_string_ptr(ptr: number): number {
    if (!ptr) return writeCString("0.0");
    return writeCString(
      softToString(
        readF128Bits(ptr),
        F128_FRAC_BITS,
        F128_EXP_BITS,
        F128_EXP_BIAS,
        F128_EXP_MAX,
        F128_MIN_EXP,
        F128_MAX_EXP,
        F128_DECIMAL_DIG,
      ),
    );
  }

  function ferret_f256_to_string_ptr(ptr: number): number {
    if (!ptr) return writeCString("0.0");
    return writeCString(
      softToString(
        readF256Bits(ptr),
        F256_FRAC_BITS,
        F256_EXP_BITS,
        F256_EXP_BIAS,
        F256_EXP_MAX,
        F256_MIN_EXP,
        F256_MAX_EXP,
        F256_DECIMAL_DIG,
      ),
    );
  }

  function ferret_i128_from_string_ptr(strPtr: number, outPtr: number): void {
    if (!outPtr) return;
    const text = strPtr ? readCString(strPtr) : "";
    const parsed = parseBigIntLiteral(text, true);
    writeBigIntSigned(outPtr, BIGINT_BITS_128, parsed ?? 0n);
  }

  function ferret_u128_from_string_ptr(strPtr: number, outPtr: number): void {
    if (!outPtr) return;
    const text = strPtr ? readCString(strPtr) : "";
    const parsed = parseBigIntLiteral(text, false);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_128, parsed ?? 0n);
  }

  function ferret_i256_from_string_ptr(strPtr: number, outPtr: number): void {
    if (!outPtr) return;
    const text = strPtr ? readCString(strPtr) : "";
    const parsed = parseBigIntLiteral(text, true);
    writeBigIntSigned(outPtr, BIGINT_BITS_256, parsed ?? 0n);
  }

  function ferret_u256_from_string_ptr(strPtr: number, outPtr: number): void {
    if (!outPtr) return;
    const text = strPtr ? readCString(strPtr) : "";
    const parsed = parseBigIntLiteral(text, false);
    writeBigIntUnsigned(outPtr, BIGINT_BITS_256, parsed ?? 0n);
  }

  function ferret_f128_from_string_ptr(strPtr: number, outPtr: number): void {
    if (!outPtr) return;
    const text = strPtr ? readCString(strPtr) : "";
    const bits = softFromDecimalString(
      text,
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
      F128_MAX_EXP,
    );
    writeF128Bits(outPtr, bits);
  }

  function ferret_f256_from_string_ptr(strPtr: number, outPtr: number): void {
    if (!outPtr) return;
    const text = strPtr ? readCString(strPtr) : "";
    const bits = softFromDecimalString(
      text,
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
      F256_MAX_EXP,
    );
    writeF256Bits(outPtr, bits);
  }

  function ferret_f128_add_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const bits = softAdd(
      readF128Bits(aPtr),
      readF128Bits(bPtr),
      false,
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
      F128_MAX_EXP,
    );
    writeF128Bits(outPtr, bits);
  }

  function ferret_f128_sub_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const bits = softAdd(
      readF128Bits(aPtr),
      readF128Bits(bPtr),
      true,
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
      F128_MAX_EXP,
    );
    writeF128Bits(outPtr, bits);
  }

  function ferret_f128_mul_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const bits = softMul(
      readF128Bits(aPtr),
      readF128Bits(bPtr),
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
      F128_MAX_EXP,
    );
    writeF128Bits(outPtr, bits);
  }

  function ferret_f128_div_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const bits = softDiv(
      readF128Bits(aPtr),
      readF128Bits(bPtr),
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
      F128_MAX_EXP,
    );
    writeF128Bits(outPtr, bits);
  }

  function ferret_f128_eq_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    const cmp = softCompare(
      readF128Bits(aPtr),
      readF128Bits(bPtr),
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
      F128_MAX_EXP,
    );
    return !cmp.unordered && cmp.cmp === 0 ? 1 : 0;
  }

  function ferret_f128_lt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    const cmp = softCompare(
      readF128Bits(aPtr),
      readF128Bits(bPtr),
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
      F128_MAX_EXP,
    );
    return !cmp.unordered && cmp.cmp < 0 ? 1 : 0;
  }

  function ferret_f128_gt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    const cmp = softCompare(
      readF128Bits(aPtr),
      readF128Bits(bPtr),
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
      F128_MAX_EXP,
    );
    return !cmp.unordered && cmp.cmp > 0 ? 1 : 0;
  }

  function ferret_f128_pow_ptr(
    basePtr: number,
    expPtr: number,
    outPtr: number,
  ) {
    if (!outPtr || !basePtr || !expPtr) return;
    const baseVal = softBitsToNumber(
      readF128Bits(basePtr),
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
    );
    const expVal = softBitsToNumber(
      readF128Bits(expPtr),
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
    );
    const bits = f64ToSoftBits(
      Math.pow(baseVal, expVal),
      F128_FRAC_BITS,
      F128_EXP_BITS,
      F128_EXP_BIAS,
      F128_EXP_MAX,
      F128_MIN_EXP,
      F128_MAX_EXP,
    );
    writeF128Bits(outPtr, bits);
  }

  function ferret_f256_add_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const bits = softAdd(
      readF256Bits(aPtr),
      readF256Bits(bPtr),
      false,
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
      F256_MAX_EXP,
    );
    writeF256Bits(outPtr, bits);
  }

  function ferret_f256_sub_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const bits = softAdd(
      readF256Bits(aPtr),
      readF256Bits(bPtr),
      true,
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
      F256_MAX_EXP,
    );
    writeF256Bits(outPtr, bits);
  }

  function ferret_f256_mul_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const bits = softMul(
      readF256Bits(aPtr),
      readF256Bits(bPtr),
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
      F256_MAX_EXP,
    );
    writeF256Bits(outPtr, bits);
  }

  function ferret_f256_div_ptr(aPtr: number, bPtr: number, outPtr: number) {
    if (!outPtr || !aPtr || !bPtr) return;
    const bits = softDiv(
      readF256Bits(aPtr),
      readF256Bits(bPtr),
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
      F256_MAX_EXP,
    );
    writeF256Bits(outPtr, bits);
  }

  function ferret_f256_eq_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    const cmp = softCompare(
      readF256Bits(aPtr),
      readF256Bits(bPtr),
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
      F256_MAX_EXP,
    );
    return !cmp.unordered && cmp.cmp === 0 ? 1 : 0;
  }

  function ferret_f256_lt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    const cmp = softCompare(
      readF256Bits(aPtr),
      readF256Bits(bPtr),
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
      F256_MAX_EXP,
    );
    return !cmp.unordered && cmp.cmp < 0 ? 1 : 0;
  }

  function ferret_f256_gt_ptr(aPtr: number, bPtr: number): number {
    if (!aPtr || !bPtr) return 0;
    const cmp = softCompare(
      readF256Bits(aPtr),
      readF256Bits(bPtr),
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
      F256_MAX_EXP,
    );
    return !cmp.unordered && cmp.cmp > 0 ? 1 : 0;
  }

  function ferret_f256_pow_ptr(
    basePtr: number,
    expPtr: number,
    outPtr: number,
  ) {
    if (!outPtr || !basePtr || !expPtr) return;
    const baseVal = softBitsToNumber(
      readF256Bits(basePtr),
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
    );
    const expVal = softBitsToNumber(
      readF256Bits(expPtr),
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
    );
    const bits = f64ToSoftBits(
      Math.pow(baseVal, expVal),
      F256_FRAC_BITS,
      F256_EXP_BITS,
      F256_EXP_BIAS,
      F256_EXP_MAX,
      F256_MIN_EXP,
      F256_MAX_EXP,
    );
    writeF256Bits(outPtr, bits);
  }

  const MAP_HASH_SEED = 0x9747b28c;

  function rotl32(value: number, shift: number): number {
    return (value << shift) | (value >>> (32 - shift));
  }

  function murmur3_32_bytes(bytes: Uint8Array, seed: number): number {
    const c1 = 0xcc9e2d51;
    const c2 = 0x1b873593;
    let h1 = seed >>> 0;

    const length = bytes.length >>> 0;
    const nblocks = (length / 4) | 0;
    for (let i = 0; i < nblocks; i++) {
      const idx = i * 4;
      let k1 =
        (bytes[idx] & 0xff) |
        ((bytes[idx + 1] & 0xff) << 8) |
        ((bytes[idx + 2] & 0xff) << 16) |
        ((bytes[idx + 3] & 0xff) << 24);

      k1 = Math.imul(k1, c1);
      k1 = rotl32(k1, 15);
      k1 = Math.imul(k1, c2);

      h1 ^= k1;
      h1 = rotl32(h1, 13);
      h1 = (Math.imul(h1, 5) + 0xe6546b64) >>> 0;
    }

    let k1 = 0;
    const tailIndex = nblocks * 4;
    switch (length & 3) {
      case 3:
        k1 ^= (bytes[tailIndex + 2] & 0xff) << 16;
      case 2:
        k1 ^= (bytes[tailIndex + 1] & 0xff) << 8;
      case 1:
        k1 ^= bytes[tailIndex] & 0xff;
        k1 = Math.imul(k1, c1);
        k1 = rotl32(k1, 15);
        k1 = Math.imul(k1, c2);
        h1 ^= k1;
    }

    h1 ^= length;
    h1 ^= h1 >>> 16;
    h1 = Math.imul(h1, 0x85ebca6b);
    h1 ^= h1 >>> 13;
    h1 = Math.imul(h1, 0xc2b2ae35);
    h1 ^= h1 >>> 16;
    return h1 >>> 0;
  }

  const zeroHash32 = murmur3_32_bytes(new Uint8Array(4), MAP_HASH_SEED);
  const zeroHash64 = murmur3_32_bytes(new Uint8Array(8), MAP_HASH_SEED);

  type FerretMapKeyKind = "i32" | "i64" | "f32" | "f64" | "str" | "bytes";
  type FerretMapEntry = { keyPtr: number; valuePtr: number; hash: number };
  type FerretMapMeta = {
    keySize: number;
    valueSize: number;
    keyKind: FerretMapKeyKind;
    buckets: Map<number, FerretMapEntry[]>;
    size: number;
  };

  // JS-side map storage: mapPtr -> metadata
  const ferretMapStore = new Map<number, FerretMapMeta>();
  const ferretMapIterStore = new Map<
    number,
    { entries: FerretMapEntry[]; index: number }
  >();

  function hashBytes(ptr: number, len: number): number {
    if (!ptr) {
      return 0;
    }
    const bytes = new Uint8Array(memory!.buffer, ptr >>> 0, len >>> 0);
    return murmur3_32_bytes(bytes, MAP_HASH_SEED);
  }

  function mapHashKey(meta: FerretMapMeta, keyPtr: number): number {
    const dv = view();
    switch (meta.keyKind) {
      case "i32":
        return hashBytes(keyPtr, 4);
      case "i64":
        return hashBytes(keyPtr, 8);
      case "f32": {
        const value = dv.getFloat32(keyPtr, true);
        if (value === 0) {
          return zeroHash32;
        }
        return hashBytes(keyPtr, 4);
      }
      case "f64": {
        const value = dv.getFloat64(keyPtr, true);
        if (value === 0) {
          return zeroHash64;
        }
        return hashBytes(keyPtr, 8);
      }
      case "str": {
        const strPtr = dv.getUint32(keyPtr, true);
        if (!strPtr) {
          return 0;
        }
        const len = ferret_string_len(strPtr);
        return hashBytes(strPtr, len);
      }
      case "bytes":
        return hashBytes(keyPtr, meta.keySize);
    }
  }

  function mapKeysEqual(
    meta: FerretMapMeta,
    keyPtr: number,
    entryKeyPtr: number,
  ): boolean {
    const dv = view();
    switch (meta.keyKind) {
      case "i32":
        return dv.getInt32(keyPtr, true) === dv.getInt32(entryKeyPtr, true);
      case "i64":
        return (
          dv.getBigInt64(keyPtr, true) === dv.getBigInt64(entryKeyPtr, true)
        );
      case "f32":
        return dv.getFloat32(keyPtr, true) === dv.getFloat32(entryKeyPtr, true);
      case "f64":
        return dv.getFloat64(keyPtr, true) === dv.getFloat64(entryKeyPtr, true);
      case "str": {
        const keyStrPtr = dv.getUint32(keyPtr, true);
        const entryStrPtr = dv.getUint32(entryKeyPtr, true);
        if (keyStrPtr === entryStrPtr) {
          return true;
        }
        if (!keyStrPtr || !entryStrPtr) {
          return keyStrPtr === entryStrPtr;
        }
        return readCString(keyStrPtr) === readCString(entryStrPtr);
      }
      case "bytes": {
        const mem = new Uint8Array(memory!.buffer);
        const a = keyPtr >>> 0;
        const b = entryKeyPtr >>> 0;
        for (let i = 0; i < meta.keySize; i++) {
          if (mem[a + i] !== mem[b + i]) {
            return false;
          }
        }
        return true;
      }
    }
  }

  function mapGetMeta(mapPtr: number): FerretMapMeta | null {
    return ferretMapStore.get(mapPtr) ?? null;
  }

  function mapSetEntry(meta: FerretMapMeta, keyPtr: number, valuePtr: number) {
    const hash = mapHashKey(meta, keyPtr);
    let bucket = meta.buckets.get(hash);
    if (!bucket) {
      bucket = [];
      meta.buckets.set(hash, bucket);
    }
    for (const entry of bucket) {
      if (mapKeysEqual(meta, keyPtr, entry.keyPtr)) {
        if (meta.valueSize > 0) {
          ferret_memcpy(entry.valuePtr, valuePtr, meta.valueSize);
        }
        return;
      }
    }
    const keyStorage = meta.keySize > 0 ? ferret_alloc(meta.keySize) : 0;
    if (meta.keySize > 0) {
      ferret_memcpy(keyStorage, keyPtr, meta.keySize);
    }
    const valueStorage = meta.valueSize > 0 ? ferret_alloc(meta.valueSize) : 0;
    if (meta.valueSize > 0) {
      ferret_memcpy(valueStorage, valuePtr, meta.valueSize);
    }
    bucket.push({ hash, keyPtr: keyStorage, valuePtr: valueStorage });
    meta.size += 1;
  }

  function mapFindEntry(
    meta: FerretMapMeta,
    keyPtr: number,
    hash: number,
  ): FerretMapEntry | null {
    const bucket = meta.buckets.get(hash);
    if (!bucket) {
      return null;
    }
    for (const entry of bucket) {
      if (mapKeysEqual(meta, keyPtr, entry.keyPtr)) {
        return entry;
      }
    }
    return null;
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
      buckets: new Map(),
      size: 0,
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
      buckets: new Map(),
      size: 0,
    });
    return mapPtr >>> 0;
  }

  function ferret_map_new_i64(keySize: number, valueSize: number): number {
    const mapPtr = ferret_alloc(8);
    ferretMapStore.set(mapPtr, {
      keySize: Number(keySize),
      valueSize: Number(valueSize),
      keyKind: "i64",
      buckets: new Map(),
      size: 0,
    });
    return mapPtr >>> 0;
  }

  function ferret_map_new_f32(keySize: number, valueSize: number): number {
    const mapPtr = ferret_alloc(8);
    ferretMapStore.set(mapPtr, {
      keySize: Number(keySize),
      valueSize: Number(valueSize),
      keyKind: "f32",
      buckets: new Map(),
      size: 0,
    });
    return mapPtr >>> 0;
  }

  function ferret_map_new_f64(keySize: number, valueSize: number): number {
    const mapPtr = ferret_alloc(8);
    ferretMapStore.set(mapPtr, {
      keySize: Number(keySize),
      valueSize: Number(valueSize),
      keyKind: "f64",
      buckets: new Map(),
      size: 0,
    });
    return mapPtr >>> 0;
  }

  function ferret_map_new_str(keySize: number, valueSize: number): number {
    const mapPtr = ferret_alloc(8);
    ferretMapStore.set(mapPtr, {
      keySize: Number(keySize),
      valueSize: Number(valueSize),
      keyKind: "str",
      buckets: new Map(),
      size: 0,
    });
    return mapPtr >>> 0;
  }

  function ferret_map_new_bytes(keySize: number, valueSize: number): number {
    const mapPtr = ferret_alloc(8);
    ferretMapStore.set(mapPtr, {
      keySize: Number(keySize),
      valueSize: Number(valueSize),
      keyKind: "bytes",
      buckets: new Map(),
      size: 0,
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

  function ferret_map_from_pairs_f32(
    keySize: number,
    valueSize: number,
    keysPtr: number,
    valuesPtr: number,
    count: number,
  ): number {
    return mapFromPairs("f32", keySize, valueSize, keysPtr, valuesPtr, count);
  }

  function ferret_map_from_pairs_f64(
    keySize: number,
    valueSize: number,
    keysPtr: number,
    valuesPtr: number,
    count: number,
  ): number {
    return mapFromPairs("f64", keySize, valueSize, keysPtr, valuesPtr, count);
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
    const hash = mapHashKey(meta, keyPtr);
    const entry = mapFindEntry(meta, keyPtr, hash);
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
    const hash = mapHashKey(meta, keyPtr);
    const entry = mapFindEntry(meta, keyPtr, hash);
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
    return meta ? meta.size : 0;
  }

  function ferret_map_iter_begin(mapPtr: number, iterPtr: number): number {
    const meta = mapGetMeta(mapPtr);
    if (!meta || meta.size === 0 || !iterPtr) {
      return 0;
    }
    const entries: FerretMapEntry[] = [];
    for (const bucket of meta.buckets.values()) {
      entries.push(...bucket);
    }
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
        ferret_i128_add_ptr,
        ferret_i128_sub_ptr,
        ferret_i128_mul_ptr,
        ferret_i128_div_ptr,
        ferret_i128_mod_ptr,
        ferret_i128_eq_ptr,
        ferret_i128_lt_ptr,
        ferret_i128_gt_ptr,
        ferret_i128_and_ptr,
        ferret_i128_or_ptr,
        ferret_i128_xor_ptr,
        ferret_i128_pow_ptr,
        ferret_u128_add_ptr,
        ferret_u128_sub_ptr,
        ferret_u128_mul_ptr,
        ferret_u128_div_ptr,
        ferret_u128_mod_ptr,
        ferret_u128_eq_ptr,
        ferret_u128_lt_ptr,
        ferret_u128_gt_ptr,
        ferret_u128_and_ptr,
        ferret_u128_or_ptr,
        ferret_u128_xor_ptr,
        ferret_u128_pow_ptr,
        ferret_i256_add_ptr,
        ferret_i256_sub_ptr,
        ferret_i256_mul_ptr,
        ferret_i256_div_ptr,
        ferret_i256_mod_ptr,
        ferret_i256_eq_ptr,
        ferret_i256_lt_ptr,
        ferret_i256_gt_ptr,
        ferret_i256_and_ptr,
        ferret_i256_or_ptr,
        ferret_i256_xor_ptr,
        ferret_i256_pow_ptr,
        ferret_u256_add_ptr,
        ferret_u256_sub_ptr,
        ferret_u256_mul_ptr,
        ferret_u256_div_ptr,
        ferret_u256_mod_ptr,
        ferret_u256_eq_ptr,
        ferret_u256_lt_ptr,
        ferret_u256_gt_ptr,
        ferret_u256_and_ptr,
        ferret_u256_or_ptr,
        ferret_u256_xor_ptr,
        ferret_u256_pow_ptr,
        ferret_f128_add_ptr,
        ferret_f128_sub_ptr,
        ferret_f128_mul_ptr,
        ferret_f128_div_ptr,
        ferret_f128_eq_ptr,
        ferret_f128_lt_ptr,
        ferret_f128_gt_ptr,
        ferret_f128_pow_ptr,
        ferret_f256_add_ptr,
        ferret_f256_sub_ptr,
        ferret_f256_mul_ptr,
        ferret_f256_div_ptr,
        ferret_f256_eq_ptr,
        ferret_f256_lt_ptr,
        ferret_f256_gt_ptr,
        ferret_f256_pow_ptr,
        ferret_i128_from_i64_ptr,
        ferret_u128_from_u64_ptr,
        ferret_i256_from_i64_ptr,
        ferret_u256_from_u64_ptr,
        ferret_f128_from_f64_ptr,
        ferret_f256_from_f64_ptr,
        ferret_i128_to_i64_ptr,
        ferret_u128_to_u64_ptr,
        ferret_i256_to_i64_ptr,
        ferret_u256_to_u64_ptr,
        ferret_f128_to_f64_ptr,
        ferret_f256_to_f64_ptr,
        ferret_i128_to_string_ptr,
        ferret_u128_to_string_ptr,
        ferret_i256_to_string_ptr,
        ferret_u256_to_string_ptr,
        ferret_f128_to_string_ptr,
        ferret_f256_to_string_ptr,
        ferret_i128_from_string_ptr,
        ferret_u128_from_string_ptr,
        ferret_i256_from_string_ptr,
        ferret_u256_from_string_ptr,
        ferret_f128_from_string_ptr,
        ferret_f256_from_string_ptr,
        ferret_map_new_i32,
        ferret_map_new_i64,
        ferret_map_new_f32,
        ferret_map_new_f64,
        ferret_map_new_str,
        ferret_map_new_bytes,
        ferret_map_from_pairs_i32,
        ferret_map_from_pairs_i64,
        ferret_map_from_pairs_f32,
        ferret_map_from_pairs_f64,
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
