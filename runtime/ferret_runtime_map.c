#include "ferret_runtime.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    const FerretTypeInfo *key_type;
    const FerretTypeInfo *value_type;
    ferret_usize len;
    ferret_usize cap;
    ferret_usize deleted;
    ferret_usize key_offset;
    ferret_usize value_offset;
    ferret_usize slot_size;
    ferret_u8   *slots;
} FerretRuntimeMap;

enum {
    FERRET_MAP_SLOT_EMPTY = 0,
    FERRET_MAP_SLOT_USED = 1,
    FERRET_MAP_SLOT_DELETED = 2,
};

static ferret_usize ferret__align_up(ferret_usize size, ferret_usize align) {
    if (align <= 1u) {
        return size;
    }
    return (size + align - 1u) & ~(align - 1u);
}

static void ferret__zero_value(void *dst, ferret_usize size) {
    if (dst != NULL && size != 0u) {
        memset(dst, 0, (size_t)size);
    }
}

static ferret_bool ferret__optional_is_none(const void *data, const FerretTypeInfo *inner) {
    ferret_u32 enum_like_none = 0xFFFFFFFFu;

    if (data == NULL || inner == NULL) {
        return 1;
    }
    if ((inner->flags & FERRET_TYPE_FLAG_POINTER) != 0u) {
        return *(void *const *)data == NULL;
    }
    switch (inner->id) {
    case FERRET_TYPE_BOOL:
        return *(const ferret_u8 *)data == FERRET_OPT_BOOL_NONE;
    case FERRET_TYPE_CHAR:
        return *(const ferret_char *)data == FERRET_OPT_CHAR_NONE;
    default:
        break;
    }
    if ((inner->flags & FERRET_TYPE_FLAG_VARIANTS) != 0u && inner->size == sizeof(ferret_u32)) {
        return *(const ferret_u32 *)data == enum_like_none;
    }
    return 0;
}

static ferret_u64 ferret__hash_bytes(const void *data, ferret_usize size) {
    const ferret_u8 *bytes = (const ferret_u8 *)data;
    ferret_u64 hash = 1469598103934665603ull;
    ferret_usize i;

    if (bytes == NULL) {
        return hash;
    }
    for (i = 0; i < size; i++) {
        hash ^= (ferret_u64)bytes[i];
        hash *= 1099511628211ull;
    }
    return hash;
}

static ferret_u64 ferret__hash_value(const void *data, const FerretTypeInfo *info);

static ferret_bool ferret__equal_value(const void *left, const void *right, const FerretTypeInfo *info) {
    ferret_usize i;

    if (info == NULL) {
        return left == right;
    }
    if (left == right) {
        return 1;
    }
    if (left == NULL || right == NULL) {
        return 0;
    }

    if (info->id == FERRET_TYPE_STR) {
        const FerretStr *a = (const FerretStr *)left;
        const FerretStr *b = (const FerretStr *)right;
        if (a->len != b->len) {
            return 0;
        }
        if (a->len == 0u) {
            return 1;
        }
        return memcmp(a->ptr, b->ptr, (size_t)a->len) == 0;
    }

    if ((info->flags & FERRET_TYPE_FLAG_ARRAY) != 0u) {
        const FerretArrayTypeInfo *meta = (const FerretArrayTypeInfo *)info->meta;
        const ferret_u8 *a = (const ferret_u8 *)left;
        const ferret_u8 *b = (const ferret_u8 *)right;
        if (meta == NULL || meta->elem == NULL) {
            return memcmp(left, right, (size_t)info->size) == 0;
        }
        for (i = 0; i < meta->len; i++) {
            if (!ferret__equal_value(a + (i * meta->stride), b + (i * meta->stride), meta->elem)) {
                return 0;
            }
        }
        return 1;
    }

    if ((info->flags & FERRET_TYPE_FLAG_TUPLE) != 0u || (info->flags & FERRET_TYPE_FLAG_STRUCT) != 0u) {
        const FerretTupleTypeInfo *meta = (const FerretTupleTypeInfo *)info->meta;
        const ferret_u8 *a = (const ferret_u8 *)left;
        const ferret_u8 *b = (const ferret_u8 *)right;
        if (meta == NULL || meta->fields == NULL) {
            return memcmp(left, right, (size_t)info->size) == 0;
        }
        for (i = 0; i < meta->len; i++) {
            const FerretTupleFieldInfo *field = &meta->fields[i];
            if (!ferret__equal_value(a + field->offset, b + field->offset, field->type)) {
                return 0;
            }
        }
        return 1;
    }

    if ((info->flags & FERRET_TYPE_FLAG_OPTIONAL) != 0u) {
        const FerretOptionalTypeInfo *meta = (const FerretOptionalTypeInfo *)info->meta;
        const ferret_u8 *a = (const ferret_u8 *)left;
        const ferret_u8 *b = (const ferret_u8 *)right;
        if (meta == NULL || meta->inner == NULL) {
            return memcmp(left, right, (size_t)info->size) == 0;
        }
        if (meta->payload_offset == 0u) {
            ferret_bool a_none = ferret__optional_is_none(left, meta->inner);
            ferret_bool b_none = ferret__optional_is_none(right, meta->inner);
            if (a_none || b_none) {
                return a_none == b_none;
            }
            return ferret__equal_value(left, right, meta->inner);
        }
        if (*(const ferret_u32 *)a != *(const ferret_u32 *)b) {
            return 0;
        }
        if (*(const ferret_u32 *)a == FERRET_NONE) {
            return 1;
        }
        return ferret__equal_value(a + meta->payload_offset, b + meta->payload_offset, meta->inner);
    }

    return memcmp(left, right, (size_t)info->size) == 0;
}

static ferret_u64 ferret__hash_value(const void *data, const FerretTypeInfo *info) {
    ferret_usize i;
    ferret_u64 hash = 1469598103934665603ull;

    if (info == NULL) {
        return ferret__hash_bytes(&data, sizeof(data));
    }
    if (data == NULL) {
        return hash;
    }

    if (info->id == FERRET_TYPE_STR) {
        const FerretStr *s = (const FerretStr *)data;
        hash ^= (ferret_u64)s->len;
        hash *= 1099511628211ull;
        if (s->len != 0u && s->ptr != NULL) {
            hash ^= ferret__hash_bytes(s->ptr, s->len);
            hash *= 1099511628211ull;
        }
        return hash;
    }

    if ((info->flags & FERRET_TYPE_FLAG_ARRAY) != 0u) {
        const FerretArrayTypeInfo *meta = (const FerretArrayTypeInfo *)info->meta;
        const ferret_u8 *bytes = (const ferret_u8 *)data;
        if (meta == NULL || meta->elem == NULL) {
            return ferret__hash_bytes(data, info->size);
        }
        for (i = 0; i < meta->len; i++) {
            hash ^= ferret__hash_value(bytes + (i * meta->stride), meta->elem);
            hash *= 1099511628211ull;
        }
        return hash;
    }

    if ((info->flags & FERRET_TYPE_FLAG_TUPLE) != 0u || (info->flags & FERRET_TYPE_FLAG_STRUCT) != 0u) {
        const FerretTupleTypeInfo *meta = (const FerretTupleTypeInfo *)info->meta;
        const ferret_u8 *bytes = (const ferret_u8 *)data;
        if (meta == NULL || meta->fields == NULL) {
            return ferret__hash_bytes(data, info->size);
        }
        for (i = 0; i < meta->len; i++) {
            const FerretTupleFieldInfo *field = &meta->fields[i];
            hash ^= ferret__hash_value(bytes + field->offset, field->type);
            hash *= 1099511628211ull;
        }
        return hash;
    }

    if ((info->flags & FERRET_TYPE_FLAG_OPTIONAL) != 0u) {
        const FerretOptionalTypeInfo *meta = (const FerretOptionalTypeInfo *)info->meta;
        const ferret_u8 *bytes = (const ferret_u8 *)data;
        if (meta == NULL || meta->inner == NULL) {
            return ferret__hash_bytes(data, info->size);
        }
        if (meta->payload_offset == 0u) {
            if (ferret__optional_is_none(data, meta->inner)) {
                return 0x9ae16a3b2f90404full;
            }
            return ferret__hash_value(data, meta->inner);
        }
        if (*(const ferret_u32 *)bytes == FERRET_NONE) {
            return 0x9ae16a3b2f90404full;
        }
        return ferret__hash_value(bytes + meta->payload_offset, meta->inner);
    }

    return ferret__hash_bytes(data, info->size);
}

static ferret_u8 *ferret__map_slot_state(FerretRuntimeMap *map, ferret_usize index) {
    return map->slots + (index * map->slot_size);
}

static ferret_u64 *ferret__map_slot_hash(FerretRuntimeMap *map, ferret_usize index) {
    return (ferret_u64 *)(map->slots + (index * map->slot_size) + sizeof(ferret_u8));
}

static void *ferret__map_slot_key(FerretRuntimeMap *map, ferret_usize index) {
    return map->slots + (index * map->slot_size) + map->key_offset;
}

static void *ferret__map_slot_value(FerretRuntimeMap *map, ferret_usize index) {
    return map->slots + (index * map->slot_size) + map->value_offset;
}

static FerretRuntimeMap *ferret__map_alloc(const FerretTypeInfo *key_type, const FerretTypeInfo *value_type, ferret_usize cap) {
    FerretRuntimeMap *map;
    ferret_usize key_align;
    ferret_usize value_align;
    ferret_usize slot_align;

    if (key_type == NULL || value_type == NULL) {
        return NULL;
    }
    if (cap < 8u) {
        cap = 8u;
    }
    while ((cap & (cap - 1u)) != 0u) {
        cap++;
    }

    map = (FerretRuntimeMap *)calloc(1u, sizeof(FerretRuntimeMap));
    if (map == NULL) {
        return NULL;
    }

    key_align = key_type->align == 0u ? 1u : key_type->align;
    value_align = value_type->align == 0u ? 1u : value_type->align;
    slot_align = value_align > key_align ? value_align : key_align;
    if (slot_align < sizeof(void *)) {
        slot_align = sizeof(void *);
    }

    map->key_type = key_type;
    map->value_type = value_type;
    map->cap = cap;
    map->key_offset = ferret__align_up(1u + sizeof(ferret_u64), key_align);
    map->value_offset = ferret__align_up(map->key_offset + key_type->size, value_align);
    map->slot_size = ferret__align_up(map->value_offset + value_type->size, slot_align);
    map->slots = (ferret_u8 *)calloc((size_t)cap, (size_t)map->slot_size);
    if (map->slots == NULL) {
        free(map);
        return NULL;
    }
    return map;
}

static ferret_bool ferret__map_should_grow(const FerretRuntimeMap *map) {
    return map != NULL && (map->len + map->deleted + 1u) * 10u >= map->cap * 7u;
}

static ferret_bool ferret__map_insert_rehashed(FerretRuntimeMap *map, const void *key, const void *value, ferret_u64 hash) {
    ferret_usize mask;
    ferret_usize index;

    if (map == NULL || map->cap == 0u) {
        return 0;
    }
    mask = map->cap - 1u;
    index = (ferret_usize)(hash & mask);
    for (;;) {
        ferret_u8 *state = ferret__map_slot_state(map, index);
        if (*state != FERRET_MAP_SLOT_USED) {
            *state = FERRET_MAP_SLOT_USED;
            *ferret__map_slot_hash(map, index) = hash;
            memcpy(ferret__map_slot_key(map, index), key, (size_t)map->key_type->size);
            memcpy(ferret__map_slot_value(map, index), value, (size_t)map->value_type->size);
            map->len++;
            return 1;
        }
        index = (index + 1u) & mask;
    }
}

static ferret_bool ferret__map_grow(ferret_raw *slot, ferret_usize min_cap) {
    FerretRuntimeMap *old_map;
    FerretRuntimeMap *new_map;
    ferret_usize i;

    if (slot == NULL) {
        return 0;
    }
    old_map = (FerretRuntimeMap *)(*slot);
    if (old_map == NULL) {
        return 0;
    }

    if (min_cap < old_map->cap * 2u) {
        min_cap = old_map->cap * 2u;
    }
    new_map = ferret__map_alloc(old_map->key_type, old_map->value_type, min_cap);
    if (new_map == NULL) {
        return 0;
    }
    for (i = 0; i < old_map->cap; i++) {
        if (*ferret__map_slot_state(old_map, i) != FERRET_MAP_SLOT_USED) {
            continue;
        }
        ferret__map_insert_rehashed(
            new_map,
            ferret__map_slot_key(old_map, i),
            ferret__map_slot_value(old_map, i),
            *ferret__map_slot_hash(old_map, i)
        );
    }
    free(old_map->slots);
    free(old_map);
    *slot = (ferret_raw)new_map;
    return 1;
}

static FerretRuntimeMap *ferret__map_ensure(ferret_raw *slot, const FerretTypeInfo *key_type, const FerretTypeInfo *value_type) {
    FerretRuntimeMap *map;

    if (slot == NULL) {
        return NULL;
    }
    map = (FerretRuntimeMap *)(*slot);
    if (map == NULL) {
        map = ferret__map_alloc(key_type, value_type, 8u);
        if (map == NULL) {
            return NULL;
        }
        *slot = (ferret_raw)map;
    } else if (ferret__map_should_grow(map)) {
        if (!ferret__map_grow(slot, map->cap * 2u)) {
            return NULL;
        }
        map = (FerretRuntimeMap *)(*slot);
    }
    return map;
}

static ferret_bool ferret__map_lookup(
    FerretRuntimeMap *map,
    const void *key,
    ferret_u64 hash,
    ferret_usize *found_index,
    ferret_usize *insert_index
) {
    ferret_usize mask;
    ferret_usize index;
    ferret_usize first_deleted = (ferret_usize)-1;

    if (found_index != NULL) {
        *found_index = (ferret_usize)-1;
    }
    if (insert_index != NULL) {
        *insert_index = (ferret_usize)-1;
    }
    if (map == NULL || map->cap == 0u) {
        return 0;
    }

    mask = map->cap - 1u;
    index = (ferret_usize)(hash & mask);
    for (;;) {
        ferret_u8 state = *ferret__map_slot_state(map, index);
        if (state == FERRET_MAP_SLOT_EMPTY) {
            if (insert_index != NULL) {
                *insert_index = first_deleted != (ferret_usize)-1 ? first_deleted : index;
            }
            return 0;
        }
        if (state == FERRET_MAP_SLOT_DELETED) {
            if (first_deleted == (ferret_usize)-1) {
                first_deleted = index;
            }
        } else if (*ferret__map_slot_hash(map, index) == hash &&
                   ferret__equal_value(ferret__map_slot_key(map, index), key, map->key_type)) {
            if (found_index != NULL) {
                *found_index = index;
            }
            if (insert_index != NULL) {
                *insert_index = index;
            }
            return 1;
        }
        index = (index + 1u) & mask;
    }
}

ferret_usize ferret_global_map_size(const ferret_raw *map) {
    const FerretRuntimeMap *runtime_map = map != NULL ? (const FerretRuntimeMap *)(*map) : NULL;
    return runtime_map != NULL ? runtime_map->len : 0u;
}

ferret_usize ferret_global_map_cap(const ferret_raw *map) {
    const FerretRuntimeMap *runtime_map = map != NULL ? (const FerretRuntimeMap *)(*map) : NULL;
    return runtime_map != NULL ? runtime_map->cap : 0u;
}

ferret_bool ferret_global_map_get(
    const ferret_raw *map,
    const void *key,
    const FerretTypeInfo *key_type,
    const FerretTypeInfo *value_type,
    void *out_value
) {
    FerretRuntimeMap *runtime_map = map != NULL ? (FerretRuntimeMap *)(*map) : NULL;
    ferret_usize found = (ferret_usize)-1;
    ferret_u64 hash;

    (void)key_type;
    if (out_value != NULL && value_type != NULL) {
        ferret__zero_value(out_value, value_type->size);
    }
    if (runtime_map == NULL || key == NULL || runtime_map->len == 0u) {
        return 0;
    }
    hash = ferret__hash_value(key, runtime_map->key_type);
    if (!ferret__map_lookup(runtime_map, key, hash, &found, NULL)) {
        return 0;
    }
    if (out_value != NULL) {
        memcpy(out_value, ferret__map_slot_value(runtime_map, found), (size_t)runtime_map->value_type->size);
    }
    return 1;
}

void ferret_global_map_get_or_panic(
    const ferret_raw *map,
    const void *key,
    const FerretTypeInfo *key_type,
    const FerretTypeInfo *value_type,
    void *out_value
) {
    if (ferret_global_map_get(map, key, key_type, value_type, out_value)) {
        return;
    }
    ferret__panic((const ferret_i8 *)"map key not found");
}

ferret_bool ferret_global_map_set(
    ferret_raw *map,
    const void *key,
    const void *value,
    const FerretTypeInfo *key_type,
    const FerretTypeInfo *value_type,
    void *out_old_value
) {
    FerretRuntimeMap *runtime_map;
    ferret_usize found = (ferret_usize)-1;
    ferret_usize insert_at = (ferret_usize)-1;
    ferret_u64 hash;

    if (out_old_value != NULL && value_type != NULL) {
        ferret__zero_value(out_old_value, value_type->size);
    }
    if (map == NULL || key == NULL || value == NULL || key_type == NULL || value_type == NULL) {
        return 0;
    }

    runtime_map = ferret__map_ensure(map, key_type, value_type);
    if (runtime_map == NULL) {
        return 0;
    }

    hash = ferret__hash_value(key, runtime_map->key_type);
    if (ferret__map_lookup(runtime_map, key, hash, &found, &insert_at)) {
        if (out_old_value != NULL) {
            memcpy(out_old_value, ferret__map_slot_value(runtime_map, found), (size_t)runtime_map->value_type->size);
        }
        memcpy(ferret__map_slot_value(runtime_map, found), value, (size_t)runtime_map->value_type->size);
        return 1;
    }

    if (insert_at == (ferret_usize)-1) {
        if (!ferret__map_grow(map, runtime_map->cap * 2u)) {
            return 0;
        }
        runtime_map = (FerretRuntimeMap *)(*map);
        ferret__map_lookup(runtime_map, key, hash, &found, &insert_at);
    }

    if (*ferret__map_slot_state(runtime_map, insert_at) == FERRET_MAP_SLOT_DELETED && runtime_map->deleted != 0u) {
        runtime_map->deleted--;
    }
    *ferret__map_slot_state(runtime_map, insert_at) = FERRET_MAP_SLOT_USED;
    *ferret__map_slot_hash(runtime_map, insert_at) = hash;
    memcpy(ferret__map_slot_key(runtime_map, insert_at), key, (size_t)runtime_map->key_type->size);
    memcpy(ferret__map_slot_value(runtime_map, insert_at), value, (size_t)runtime_map->value_type->size);
    runtime_map->len++;
    return 0;
}
