# Backend ABI Contract

This document freezes the contract between Ferret MIR and backend-specific IR targets.

## Goals
- MIR remains backend-independent.
- QBE and LLVM lower from the same MIR + layout contract.
- Backend lowering must not depend on AST, HIR, parser state, or typechecker re-derivation.

## Backend Input
Each backend consumes:
- MIR module
- computed layout module
- symbol/linkage metadata already attached to MIR declarations

Backends must not inspect frontend AST nodes to recover semantic information.

## Calling Convention Boundary
The backend contract must define:
- parameter passing
- return passing
- extern symbol naming
- panic/runtime entrypoints
- aggregate layout usage

This contract is not fully implemented yet, but all backend work must preserve the separation.

## Current Policy
- MIR is the only executable IR lowered into backend targets.
- layout is the only source of physical size/alignment/offset information.
- HIR is not a backend input.
- backend IR shape must not influence MIR design.

## Target Backends
Planned:
- QBE
- LLVM

QBE is vendored under `internal/backend/qbe/qbe-src` from the official upstream repository.
