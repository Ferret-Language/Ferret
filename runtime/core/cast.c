// Ferret runtime: Type casting helpers

#include <stdint.h>

#define FERRET_CAST_FUNC(from_type, to_type, name_suffix) \
    to_type ferret_cast_##from_type##_to_##name_suffix(from_type value) { \
        return (to_type)value; \
    }

FERRET_CAST_FUNC(uint32_t, float, f32)
FERRET_CAST_FUNC(uint32_t, double, f64)
FERRET_CAST_FUNC(uint64_t, float, f32)
FERRET_CAST_FUNC(uint64_t, double, f64)
FERRET_CAST_FUNC(float, uint32_t, u32)
FERRET_CAST_FUNC(double, uint32_t, u32)
FERRET_CAST_FUNC(float, uint64_t, u64)
FERRET_CAST_FUNC(double, uint64_t, u64)
