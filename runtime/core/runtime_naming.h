#ifndef FERRET_RUNTIME_NAMING_H
#define FERRET_RUNTIME_NAMING_H

// Ferret runtime naming convention macros
// Pattern: ferret_MODULE_PATH_FUNCTION_NAME
// 
// For a function "Add" in ferret_libs/math/bigint.fer -> ferret_math_bigint_Add
// For a function "Exit" in ferret_libs/os.fer -> ferret_os_Exit
//
// Usage:
//   1. Define the module prefix at top of file: #define MODULE_PREFIX ferret_math_bigint
//   2. Use FERRET_FUNC(name) macro: FERRET_FUNC(Add) -> ferret_math_bigint_Add

// Preprocessor concatenation helpers  
#define FERRET_CONCAT_IMPL(a, b) a##_##b
#define FERRET_CONCAT(a, b) FERRET_CONCAT_IMPL(a, b)

// Main function naming macro - concatenates MODULE_PREFIX with function name
#define FERRET_FUNC(name) FERRET_CONCAT(MODULE_PREFIX, name)

// Alternative explicit macro for when you want to specify the full path
#define FERRET_FUNC_EXPLICIT(module_path, name) FERRET_CONCAT(ferret_##module_path, name)

// Type naming macro - useful for structs/enums that match Ferret types
#define FERRET_TYPE(name) FERRET_CONCAT(MODULE_PREFIX, name)

#endif // FERRET_RUNTIME_NAMING_H