/* SPDX-License-Identifier: (LGPL-2.1 OR BSD-2-Clause) */
/* Minimal bpf_helpers.h for uprobe programs */

#ifndef __BPF_HELPERS_H
#define __BPF_HELPERS_H

/* Helper macro to place programs, maps, license in
 * different sections in elf_bpf file. Section names
 * are interpreted by elf_bpf loader
 */
#define SEC(NAME) __attribute__((section(NAME), used))

/* Map definition macros */
#define __uint(name, val) int (*name)[val]
#define __type(name, val) typeof(val) *name
#define __array(name, val) typeof(val) *name[]

/* BPF map types */
#define BPF_MAP_TYPE_HASH 1
#define BPF_MAP_TYPE_RINGBUF 27

/* BPF map update flags */
#define BPF_ANY 0

/* Helper functions */
static unsigned long long (*bpf_ktime_get_ns)(void) = (void *) 5;
static long (*bpf_map_update_elem)(void *map, const void *key, const void *value, unsigned long long flags) = (void *) 2;
static void *(*bpf_map_lookup_elem)(void *map, const void *key) = (void *) 1;
static long (*bpf_map_delete_elem)(void *map, const void *key) = (void *) 3;
static unsigned long long (*bpf_get_current_pid_tgid)(void) = (void *) 14;

/* Ring buffer helpers */
static void *(*bpf_ringbuf_reserve)(void *ringbuf, unsigned long long size, unsigned long long flags) = (void *) 131;
static void (*bpf_ringbuf_submit)(void *data, unsigned long long flags) = (void *) 132;

#endif /* __BPF_HELPERS_H */
