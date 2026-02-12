#include "all.h"

#include <stdbool.h>

typedef enum ArgPassStyle {
  APS_Invalid = 0,
  APS_Register,
  APS_InlineOnStack,
  APS_CopyAndPointerInRegister,
  APS_CopyAndPointerOnStack,
  APS_VarargsTag,
  APS_EnvTag,
} ArgPassStyle;

typedef struct ArgClass {
  Typ* type;
  ArgPassStyle style;
  int align;
  uint size;
  int cls;
  Ref ref;
} ArgClass;

typedef struct ExtraAlloc ExtraAlloc;
struct ExtraAlloc {
  Ins instr;
  ExtraAlloc* link;
};

#define ALIGN_DOWN(n, a) ((n) & ~((a)-1))
#define ALIGN_UP(n, a) ALIGN_DOWN((n) + (a)-1, (a))

// Number of stack bytes required be reserved for the callee.
#define SHADOW_SPACE_SIZE 32

int amd64_winabi_rsave[] = {RCX,  RDX,   R8,    R9,    R10,   R11,   RAX,  XMM0,
                            XMM1, XMM2,  XMM3,  XMM4,  XMM5,  XMM6,  XMM7, XMM8,
                            XMM9, XMM10, XMM11, XMM12, XMM13, XMM14, -1};
int amd64_winabi_rclob[] = {RBX, R12, R13, R14, R15, RSI, RDI, -1};

MAKESURE(winabi_arrays_ok,
         sizeof amd64_winabi_rsave == (NGPS_WIN + NFPS + 1) * sizeof(int) &&
             sizeof amd64_winabi_rclob == (NCLR_WIN + 1) * sizeof(int));

// layout of call's second argument (RCall)
//
// bit 0: rax returned
// bit 1: xmm0 returned
// bits 23: 0
// bits 4567: rcx, rdx, r8, r9 passed
// bits 89ab: xmm0,1,2,3 passed
// bit c: env call (rax passed)
// bits d..1f: 0

bits amd64_winabi_retregs(Ref r, int p[2]) {
  assert(rtype(r) == RCall);

  bits b = 0;
  int num_int_returns = r.val & 1;
  int num_float_returns = r.val & 2;
  if (num_int_returns == 1) {
    b |= BIT(RAX);
  } else {
    b |= BIT(XMM0);
  }
  if (p) {
    p[0] = num_int_returns;
    p[1] = num_float_returns;
  }
  return b;
}

static uint popcnt(bits b) {
  b = (b & 0x5555555555555555) + ((b >> 1) & 0x5555555555555555);
  b = (b & 0x3333333333333333) + ((b >> 2) & 0x3333333333333333);
  b = (b & 0x0f0f0f0f0f0f0f0f) + ((b >> 4) & 0x0f0f0f0f0f0f0f0f);
  b += (b >> 8);
  b += (b >> 16);
  b += (b >> 32);
  return b & 0xff;
}

bits amd64_winabi_argregs(Ref r, int p[2]) {
  assert(rtype(r) == RCall);

  // On SysV, these are counts. Here, a count isn't sufficient, we actually need
  // to know which ones are in use because they're not necessarily contiguous.
  int int_passed = (r.val >> 4) & 15;
  int float_passed = (r.val >> 8) & 15;
  bool env_param = (r.val >> 12) & 1;

  bits b = 0;
  b |= (int_passed & 1) ? BIT(RCX) : 0;
  b |= (int_passed & 2) ? BIT(RDX) : 0;
  b |= (int_passed & 4) ? BIT(R8) : 0;
  b |= (int_passed & 8) ? BIT(R9) : 0;
  b |= (float_passed & 1) ? BIT(XMM0) : 0;
  b |= (float_passed & 2) ? BIT(XMM1) : 0;
  b |= (float_passed & 4) ? BIT(XMM2) : 0;
  b |= (float_passed & 8) ? BIT(XMM3) : 0;
  b |= env_param ? BIT(RAX) : 0;
  if (p) {
    // TODO: The only place this is used is live.c. I'm not sure what should be
    // returned here wrt to using the same counter for int/float regs on win.
    // For now, try the number of registers in use even though they're not
    // contiguous.
    p[0] = popcnt(int_passed);
    p[1] = popcnt(float_passed);
  }
  return b;
}

typedef struct RegisterUsage {
  // Counter for both int/float as they're counted together. Only if the bool's
  // set in regs_passed is the given register *actually* needed for a value
  // (i.e. needs to be saved, etc.).
  int num_regs_passed;

  // Indexed first by 0=int, 1=float, use KBASE(cls).
  // Indexed second by register index in calling convention, so for integer,
  // 0=RCX, 1=RDX, 2=R8, 3=R9, and for float XMM0, XMM1, XMM2, XMM3.
  bool regs_passed[2][4];

  bool rax_returned;
  bool xmm0_returned;

  // This is also used as where the va_start will start for varargs functions
  // (there's no 'Oparv', so we need to keep track of a count here.)
  int num_named_args_passed;

  // This is set when classifying the arguments for a call (but not when
  // classifying the parameters of a function definition).
  bool is_varargs_call;

  bool has_env;
} RegisterUsage;

static int register_usage_to_call_arg_value(RegisterUsage reg_usage) {
  return (reg_usage.rax_returned << 0) |        //
         (reg_usage.xmm0_returned << 1) |       //
         (reg_usage.regs_passed[0][0] << 4) |   //
         (reg_usage.regs_passed[0][1] << 5) |   //
         (reg_usage.regs_passed[0][2] << 6) |   //
         (reg_usage.regs_passed[0][3] << 7) |   //
         (reg_usage.regs_passed[1][0] << 8) |   //
         (reg_usage.regs_passed[1][1] << 9) |   //
         (reg_usage.regs_passed[1][2] << 10) |  //
         (reg_usage.regs_passed[1][3] << 11) |  //
         (reg_usage.has_env << 12);
}

// Assigns the argument to a register if there's any left according to the
// calling convention, and updates the regs_passed bools. Otherwise marks the
// value as needing stack space to be passed.
static void assign_register_or_stack(RegisterUsage* reg_usage,
                                     ArgClass* arg,
                                     bool is_float,
                                     bool by_copy) {
  if (reg_usage->num_regs_passed == 4) {
    arg->style = by_copy ? APS_CopyAndPointerOnStack : APS_InlineOnStack;
  } else {
    reg_usage->regs_passed[is_float][reg_usage->num_regs_passed] = true;
    ++reg_usage->num_regs_passed;
    arg->style = by_copy ? APS_CopyAndPointerInRegister : APS_Register;
  }
  ++reg_usage->num_named_args_passed;
}

static bool type_is_by_copy(Typ* type) {
  // Note that only these sizes are passed by register, even though e.g. a
  // 5 byte struct would "fit", it still is passed by copy-and-pointer.
  return type->isdark || (type->size != 1 && type->size != 2 &&
                          type->size != 4 && type->size != 8);
}

// This function is used for both arguments and parameters.
// begin_instr should either point at the first Oarg or Opar, and end_instr
// should point past the last one (so to the Ocall for arguments, or to the
// first 'real' instruction of the function for parameters).
static void classify_arguments(RegisterUsage* reg_usage,
                               Ins* begin_instr,
                               Ins* end_instr,
                               ArgClass* arg_classes,
                               Ref* env) {
  ArgClass* arg = arg_classes;
  // For each argument, determine how it will be passed (int, float, stack)
  // and update the `reg_usage` counts. Additionally, fill out arg_classes for
  // each argument.
  for (Ins* instr = begin_instr; instr < end_instr; ++instr, ++arg) {
    switch (instr->op) {
      case Oarg:
      case Opar:
        assign_register_or_stack(reg_usage, arg, KBASE(instr->cls),
                                 /*by_copy=*/false);
        arg->cls = instr->cls;
        arg->align = 3;
        arg->size = 8;
        break;
      case Oargc:
      case Oparc: {
        int typ_index = instr->arg[0].val;
        Typ* type = &typ[typ_index];
        bool by_copy = type_is_by_copy(type);
        assign_register_or_stack(reg_usage, arg, /*is_float=*/false, by_copy);
        arg->cls = Kl;
        if (!by_copy && type->size <= 4) {
          arg->cls = Kw;
        }
        arg->align = 3;
        arg->size = type->size;
        break;
      }
      case Oarge:
        *env = instr->arg[0];
        arg->style = APS_EnvTag;
        reg_usage->has_env = true;
        break;
      case Opare:
        *env = instr->to;
        arg->style = APS_EnvTag;
        reg_usage->has_env = true;
        break;
      case Oargv:
        reg_usage->is_varargs_call = true;
        arg->style = APS_VarargsTag;
        break;
    }
  }

  if (reg_usage->has_env && reg_usage->is_varargs_call) {
    die("can't use env with varargs");
  }

  // During a varargs call, float arguments have to be duplicated to their
  // associated integer register, so mark them as in-use too.
  if (reg_usage->is_varargs_call) {
    for (int i = 0; i < 4; ++i) {
      if (reg_usage->regs_passed[/*float*/ 1][i]) {
        reg_usage->regs_passed[/*int*/ 0][i] = true;
      }
    }
  }
}

static bool is_integer_type(int ty) {
  assert(ty >= 0 && ty < 4 && "expecting Kw Kl Ks Kd");
  return KBASE(ty) == 0;
}

static Ref register_for_arg(int cls, int counter) {
  assert(counter < 4);
  if (is_integer_type(cls)) {
    return TMP(amd64_winabi_rsave[counter]);
  } else {
    return TMP(XMM0 + counter);
  }
}

static Ins* lower_call(Fn* func,
                       Blk* block,
                       Ins* call_instr,
                       ExtraAlloc** pextra_alloc) {
  // Call arguments are instructions. Walk through them to find the end of the
  // call+args that we need to process (and return the instruction past the body
  // of the instruction for continuing processing).
  Ins* instr_past_args = call_instr - 1;
  for (; instr_past_args >= block->ins; --instr_past_args) {
    if (!isarg(instr_past_args->op)) {
      break;
    }
  }
  Ins* earliest_arg_instr = instr_past_args + 1;

  // Don't need an ArgClass for the call itself, so one less than the total
  // number of instructions we're dealing with.
  uint num_args = call_instr - earliest_arg_instr;
  ArgClass* arg_classes = alloc(num_args * sizeof(ArgClass));

  RegisterUsage reg_usage = {0};
  ArgClass ret_arg_class = {0};

  // Ocall's two arguments are the the function to be called in 0, and, if the
  // the function returns a non-basic type, then arg[1] is a reference to the
  // type of the return. req checks if Refs are equal; `R` is 0.
  bool il_has_struct_return = !req(call_instr->arg[1], R);
  bool is_struct_return = false;
  if (il_has_struct_return) {
    Typ* ret_type = &typ[call_instr->arg[1].val];
    is_struct_return = type_is_by_copy(ret_type);
    if (is_struct_return) {
      assign_register_or_stack(&reg_usage, &ret_arg_class, /*is_float=*/false,
                               /*by_copy=*/true);
    }
    ret_arg_class.size = ret_type->size;
  }
  Ref env = R;
  classify_arguments(&reg_usage, earliest_arg_instr, call_instr, arg_classes,
                     &env);

  // We now know which arguments are on the stack and which are in registers, so
  // we can allocate the correct amount of space to stash the stack-located ones
  // into.
  uint stack_usage = 0;
  for (uint i = 0; i < num_args; ++i) {
    ArgClass* arg = &arg_classes[i];
    // stack_usage only accounts for pushes that are for values that don't have
    // enough registers. Large struct copies are alloca'd separately, and then
    // only have (potentially) 8 bytes to add to stack_usage here.
    if (arg->style == APS_InlineOnStack) {
      if (arg->align > 4) {
        err("win abi cannot pass alignments > 16");
      }
      stack_usage += arg->size;
    } else if (arg->style == APS_CopyAndPointerOnStack) {
      stack_usage += 8;
    }
  }
  stack_usage = ALIGN_UP(stack_usage, 16);

  // Note that here we're logically 'after' the call (due to emitting
  // instructions in reverse order), so we're doing a negative stack
  // allocation to clean up after the call.
  Ref stack_size_ref =
      getcon(-(int64_t)(stack_usage + SHADOW_SPACE_SIZE), func);
  emit(Osalloc, Kl, R, stack_size_ref, R);

  ExtraAlloc* return_pad = NULL;
  if (is_struct_return) {
    return_pad = alloc(sizeof(ExtraAlloc));
    Ref ret_pad_ref = newtmp("abi.ret_pad", Kl, func);
    return_pad->instr =
        (Ins){Oalloc8, Kl, ret_pad_ref, {getcon(ret_arg_class.size, func)}};
    return_pad->link = (*pextra_alloc);
    *pextra_alloc = return_pad;
    reg_usage.rax_returned = true;
    emit(Ocopy, call_instr->cls, call_instr->to, TMP(RAX), R);
  } else {
    if (il_has_struct_return) {
      // In the case that at the IL level, a struct return was specified, but as
      // far as the calling convention is concerned it's not actually by
      // pointer, we need to store the return value into an alloca because
      // subsequent IL will still be treating the function return as a pointer.
      ExtraAlloc* return_copy = alloc(sizeof(ExtraAlloc));
      return_copy->instr =
          (Ins){Oalloc8, Kl, call_instr->to, {getcon(8, func)}};
      return_copy->link = (*pextra_alloc);
      *pextra_alloc = return_copy;
      Ref copy = newtmp("abi.copy", Kl, func);
      emit(Ostorel, 0, R, copy, call_instr->to);
      emit(Ocopy, Kl, copy, TMP(RAX), R);
      reg_usage.rax_returned = true;
    } else if (is_integer_type(call_instr->cls)) {
      // Only a basic type returned from the call, integer.
      emit(Ocopy, call_instr->cls, call_instr->to, TMP(RAX), R);
      reg_usage.rax_returned = true;
    } else {
      // Basic type, floating point.
      emit(Ocopy, call_instr->cls, call_instr->to, TMP(XMM0), R);
      reg_usage.xmm0_returned = true;
    }
  }

  // Emit the actual call instruction. There's no 'to' value by this point
  // because we've lowered it into register manipulation (that's the `R`),
  // arg[0] of the call is the function, and arg[1] is register usage is
  // documented as above (copied from SysV).
  emit(Ocall, call_instr->cls, R, call_instr->arg[0],
       CALL(register_usage_to_call_arg_value(reg_usage)));

  if (!req(R, env)) {
    // If there's an env arg to be passed, it gets stashed in RAX.
    emit(Ocopy, Kl, TMP(RAX), env, R);
  }

  if (reg_usage.is_varargs_call) {
    // Any float arguments need to be duplicated to integer registers. This is
    // required by the calling convention so that dumping to shadow space can be
    // done without a prototype and for varargs.
#define DUP_IF_USED(index, floatreg, intreg)        \
  if (reg_usage.regs_passed[/*float*/ 1][index]) {  \
    emit(Ocast, Kl, TMP(intreg), TMP(floatreg), R); \
  }
    DUP_IF_USED(0, XMM0, RCX);
    DUP_IF_USED(1, XMM1, RDX);
    DUP_IF_USED(2, XMM2, R8);
    DUP_IF_USED(3, XMM3, R9);
#undef DUP_IF_USED
  }

  int reg_counter = 0;
  if (is_struct_return) {
    Ref first_reg = register_for_arg(Kl, reg_counter++);
    emit(Ocopy, Kl, first_reg, return_pad->instr.to, R);
  }

  // This is where we actually do the load of values into registers or into
  // stack slots.
  Ref arg_stack_slots = newtmp("abi.args", Kl, func);
  uint slot_offset = SHADOW_SPACE_SIZE;
  ArgClass* arg = arg_classes;
  for (Ins* instr = earliest_arg_instr; instr != call_instr; ++instr, ++arg) {
    switch (arg->style) {
      case APS_Register: {
        Ref into = register_for_arg(arg->cls, reg_counter++);
        if (instr->op == Oargc) {
          // If this is a small struct being passed by value. The value in the
          // instruction in this case is a pointer, but it needs to be loaded
          // into the register.
          emit(Oload, arg->cls, into, instr->arg[1], R);
        } else {
          // Otherwise, a normal value passed in a register.
          emit(Ocopy, instr->cls, into, instr->arg[0], R);
        }
        break;
      }
      case APS_InlineOnStack: {
        Ref slot = newtmp("abi.off", Kl, func);
        if (instr->op == Oargc) {
          // This is a small struct, so it's not passed by copy, but the
          // instruction is a pointer. So we need to copy it into the stack
          // slot. (And, remember that these are emitted backwards, so store,
          // then load.)
          Ref smalltmp = newtmp("abi.smalltmp", arg->cls, func);
          emit(Ostorel, 0, R, smalltmp, slot);
          emit(Oload, arg->cls, smalltmp, instr->arg[1], R);
        } else {
          // Stash the value into the stack slot.
          emit(Ostorel, 0, R, instr->arg[0], slot);
        }
        emit(Oadd, Kl, slot, arg_stack_slots, getcon(slot_offset, func));
        slot_offset += arg->size;
        break;
      }
      case APS_CopyAndPointerInRegister:
      case APS_CopyAndPointerOnStack: {
        // Alloca a space to copy into, and blit the value from the instr to the
        // copied location.
        ExtraAlloc* arg_copy = alloc(sizeof(ExtraAlloc));
        Ref copy_ref = newtmp("abi.copy", Kl, func);
        arg_copy->instr =
            (Ins){Oalloc8, Kl, copy_ref, {getcon(arg->size, func)}};
        arg_copy->link = (*pextra_alloc);
        *pextra_alloc = arg_copy;
        emit(Oblit1, 0, R, INT(arg->size), R);
        emit(Oblit0, 0, R, instr->arg[1], copy_ref);

        // Now load the pointer into the correct register or stack slot.
        if (arg->style == APS_CopyAndPointerInRegister) {
          Ref into = register_for_arg(arg->cls, reg_counter++);
          emit(Ocopy, Kl, into, copy_ref, R);
        } else {
          assert(arg->style == APS_CopyAndPointerOnStack);
          Ref slot = newtmp("abi.off", Kl, func);
          emit(Ostorel, 0, R, copy_ref, slot);
          emit(Oadd, Kl, slot, arg_stack_slots, getcon(slot_offset, func));
          slot_offset += 8;
        }
        break;
      }
      case APS_EnvTag:
      case APS_VarargsTag:
        // Nothing to do here, see right before the call for reg dupe.
        break;
      case APS_Invalid:
        die("unreachable");
    }
  }

  if (stack_usage) {
    // The last (first in call order) thing we do is allocate the the stack
    // space we're going to fill with temporaries.
    emit(Osalloc, Kl, arg_stack_slots,
         getcon(stack_usage + SHADOW_SPACE_SIZE, func), R);
  } else {
    // When there's no usage for temporaries, we can add this into the other
    // alloca, but otherwise emit it separately (not storing into a reference)
    // so that it doesn't get removed later for being useless.
    emit(Osalloc, Kl, R, getcon(SHADOW_SPACE_SIZE, func), R);
  }

  return instr_past_args;
}

static void lower_block_return(Fn* func, Blk* block) {
  int jmp_type = block->jmp.type;

  if (!isret(jmp_type) || jmp_type == Jret0) {
    return;
  }

  // Save the argument, and set the block to be a void return because once it's
  // lowered it's handled by the the register/stack manipulation.
  Ref ret_arg = block->jmp.arg;
  block->jmp.type = Jret0;

  RegisterUsage reg_usage = {0};

  if (jmp_type == Jretc) {
    Typ* type = &typ[func->retty];
    if (type_is_by_copy(type)) {
      assert(rtype(func->retr) == RTmp);
      emit(Ocopy, Kl, TMP(RAX), func->retr, R);
      emit(Oblit1, 0, R, INT(type->size), R);
      emit(Oblit0, 0, R, ret_arg, func->retr);
    } else {
      emit(Oload, Kl, TMP(RAX), ret_arg, R);
    }
    reg_usage.rax_returned = true;
  } else {
    int k = jmp_type - Jretw;
    if (is_integer_type(k)) {
      emit(Ocopy, k, TMP(RAX), ret_arg, R);
      reg_usage.rax_returned = true;
    } else {
      emit(Ocopy, k, TMP(XMM0), ret_arg, R);
      reg_usage.xmm0_returned = true;
    }
  }
  block->jmp.arg = CALL(register_usage_to_call_arg_value(reg_usage));
}

static void lower_vastart(Fn* func,
                          RegisterUsage* param_reg_usage,
                          Ref valist) {
  assert(func->vararg);
  // In varargs functions:
  // 1. the int registers are already dumped to the shadow stack space;
  // 2. any parameters passed in floating point registers have
  //    been duplicated to the integer registers
  // 3. we ensure (later) that for varargs functions we're always using an rbp
  //    frame pointer.
  // So, the ... argument is just indexed past rbp by the number of named values
  // that were actually passed.

  Ref offset = newtmp("abi.vastart", Kl, func);
  emit(Ostorel, 0, R, offset, valist);

  // *8 for sizeof(u64), +16 because the return address and rbp have been pushed
  // by the time we get to the body of the function.
  emit(Oadd, Kl, offset, TMP(RBP),
       getcon(param_reg_usage->num_named_args_passed * 8 + 16, func));
}

static void lower_vaarg(Fn* func, Ins* vaarg_instr) {
  // va_list is just a void** on winx64, so load the pointer, then load the
  // argument from that pointer, then increment the pointer to the next arg.
  // (All emitted backwards as usual.)
  Ref inc = newtmp("abi.vaarg.inc", Kl, func);
  Ref ptr = newtmp("abi.vaarg.ptr", Kl, func);
  emit(Ostorel, 0, R, inc, vaarg_instr->arg[0]);
  emit(Oadd, Kl, inc, ptr, getcon(8, func));
  emit(Oload, vaarg_instr->cls, vaarg_instr->to, ptr, R);
  emit(Oload, Kl, ptr, vaarg_instr->arg[0], R);
}

static void lower_args_for_block(Fn* func,
                                 Blk* block,
                                 RegisterUsage* param_reg_usage,
                                 ExtraAlloc** pextra_alloc) {
  // global temporary buffer used by emit. Reset to the end, and predecremented
  // when adding to it.
  curi = &insb[NIns];

  lower_block_return(func, block);

  if (block->nins) {
    // Work backwards through the instructions, either copying them unchanged,
    // or modifying as necessary.
    for (Ins* instr = &block->ins[block->nins - 1]; instr >= block->ins;) {
      switch (instr->op) {
        case Ocall:
          instr = lower_call(func, block, instr, pextra_alloc);
          break;
        case Ovastart:
          lower_vastart(func, param_reg_usage, instr->arg[0]);
          --instr;
          break;
        case Ovaarg:
          lower_vaarg(func, instr);
          --instr;
          break;
        case Oarg:
        case Oargc:
          die("unreachable");
        default:
          emiti(*instr);
          --instr;
          break;
      }
    }
  }

  // This it the start block, which is processed last. Add any allocas that
  // other blocks needed.
  bool is_start_block = block == func->start;
  if (is_start_block) {
    for (ExtraAlloc* ea = *pextra_alloc; ea; ea = ea->link) {
      emiti(ea->instr);
    }
  }

  // emit/emiti add instructions from the end to the beginning of the temporary
  // global buffer. dup the final version into the final block storage.
  block->nins = &insb[NIns] - curi;
  idup(block, curi, block->nins);
}

static Ins* find_end_of_func_parameters(Blk* start_block) {
  Ins* i;
  for (i = start_block->ins; i < &start_block->ins[start_block->nins]; ++i) {
    if (!ispar(i->op)) {
      break;
    }
  }
  return i;
}

// Copy from registers/stack into values.
static RegisterUsage lower_func_parameters(Fn* func) {
  // This is half-open, so end points after the last Opar.
  Blk* start_block = func->start;
  Ins* start_of_params = start_block->ins;
  Ins* end_of_params = find_end_of_func_parameters(start_block);

  size_t num_params = end_of_params - start_of_params;
  ArgClass* arg_classes = alloc(num_params * sizeof(ArgClass));
  ArgClass arg_ret = {0};

  // global temporary buffer used by emit. Reset to the end, and predecremented
  // when adding to it.
  curi = &insb[NIns];

  int reg_counter = 0;
  RegisterUsage reg_usage = {0};
  if (func->retty >= 0) {
    bool by_copy = type_is_by_copy(&typ[func->retty]);
    if (by_copy) {
      assign_register_or_stack(&reg_usage, &arg_ret, /*is_float=*/false,
                               by_copy);
      Ref ret_ref = newtmp("abi.ret", Kl, func);
      emit(Ocopy, Kl, ret_ref, TMP(RCX), R);
      func->retr = ret_ref;
      ++reg_counter;
    }
  }
  Ref env = R;
  classify_arguments(&reg_usage, start_of_params, end_of_params, arg_classes,
                     &env);
  func->reg = amd64_winabi_argregs(
      CALL(register_usage_to_call_arg_value(reg_usage)), NULL);

  // Copy from the registers or stack slots into the named parameters. Depending
  // on how they're passed, they either need to be copied or loaded.
  ArgClass* arg = arg_classes;
  uint slot_offset = SHADOW_SPACE_SIZE / 4 + 4;
  for (Ins* instr = start_of_params; instr < end_of_params; ++instr, ++arg) {
    switch (arg->style) {
      case APS_Register: {
        Ref from = register_for_arg(arg->cls, reg_counter++);
        // If it's a struct at the IL level, we need to copy the register into
        // an alloca so we have something to point at (same for InlineOnStack).
        if (instr->op == Oparc) {
          arg->ref = newtmp("abi", Kl, func);
          emit(Ostorel, 0, R, arg->ref, instr->to);
          emit(Ocopy, instr->cls, arg->ref, from, R);
          emit(Oalloc8, Kl, instr->to, getcon(arg->size, func), R);
        } else {
          emit(Ocopy, instr->cls, instr->to, from, R);
        }
        break;
      }
      case APS_InlineOnStack:
        if (instr->op == Oparc) {
          arg->ref = newtmp("abi", Kl, func);
          emit(Ostorel, 0, R, arg->ref, instr->to);
          emit(Ocopy, instr->cls, arg->ref, SLOT(-slot_offset), R);
          emit(Oalloc8, Kl, instr->to, getcon(arg->size, func), R);
        } else {
          emit(Ocopy, Kl, instr->to, SLOT(-slot_offset), R);
        }
        slot_offset += 2;
        break;
      case APS_CopyAndPointerOnStack:
        emit(Oload, Kl, instr->to, SLOT(-slot_offset), R);
        slot_offset += 2;
        break;
      case APS_CopyAndPointerInRegister: {
        // Because this has to be a copy (that we own), it is sufficient to just
        // copy the register to the target.
        Ref from = register_for_arg(Kl, reg_counter++);
        emit(Ocopy, Kl, instr->to, from, R);
        break;
      }
      case APS_EnvTag:
        break;
      case APS_VarargsTag:
      case APS_Invalid:
        die("unreachable");
    }
  }

  // If there was an `env`, it was passed in RAX, so copy it into the env ref.
  if (!req(R, env)) {
    emit(Ocopy, Kl, env, TMP(RAX), R);
  }

  int num_created_instrs = &insb[NIns] - curi;
  int num_other_after_instrs = (int)(start_block->nins - num_params);
  int new_total_instrs = num_other_after_instrs + num_created_instrs;
  Ins* new_instrs = vnew(new_total_instrs, sizeof(Ins), PFn);
  Ins* instr_p = icpy(new_instrs, curi, num_created_instrs);
  icpy(instr_p, end_of_params, num_other_after_instrs);
  start_block->nins = new_total_instrs;
  start_block->ins = new_instrs;

  return reg_usage;
}

// The main job of this function is to lower generic instructions into the
// specific details of how arguments are passed, and parameters are
// interpreted for win x64. A useful reference is
// https://learn.microsoft.com/en-us/cpp/build/x64-calling-convention .
//
// Some of the major differences from SysV if you're comparing the code
// (non-exhaustive):
// - only 4 int and 4 float regs are used
// - when an int register is assigned a value, its associated float register is
//   left unused (and vice versa). i.e. there's only one counter as you assign
//   arguments to registers.
// - any structs that aren't 1/2/4/8 bytes in size are passed by pointer, not
//   by copying them into the stack. So e.g. if you pass something like
//   `struct { void*, int64_t }` by value, it first needs to be copied to
//   another alloca (in order to maintain value semantics at the language
//   level), then the pointer to that copy is treated as a regular integer
//   argument (which then itself may *also* be copied to the stack in the case
//   there's no integer register remaining.)
// - when calling a varargs functions, floating point values must be duplicated
//   integer registers. Along with the above restrictions, this makes varargs
//   handling simpler for the callee than SysV.
void amd64_winabi_abi(Fn* func) {
  // The first thing to do is lower incoming parameters to this function.
  RegisterUsage param_reg_usage = lower_func_parameters(func);

  // This is the second larger part of the job. We walk all blocks, and rewrite
  // instructions returns, calls, and handling of varargs into their win x64
  // specific versions. Any other instructions are just passed through unchanged
  // by using `emiti`.

  // Skip over the entry block, and do it at the end so that our later
  // modifications can add allocations to the start block. In particular, we
  // need to add stack allocas for copies when structs are passed or returned by
  // value.
  ExtraAlloc* extra_alloc = NULL;
  for (Blk* block = func->start->link; block; block = block->link) {
    lower_args_for_block(func, block, &param_reg_usage, &extra_alloc);
  }
  lower_args_for_block(func, func->start, &param_reg_usage, &extra_alloc);

  if (debug['A']) {
    fprintf(stderr, "\n> After ABI lowering:\n");
    printfn(func, stderr);
  }
}
