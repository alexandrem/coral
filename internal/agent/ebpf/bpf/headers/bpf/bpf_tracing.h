/* SPDX-License-Identifier: (LGPL-2.1 OR BSD-2-Clause) */
/* Minimal bpf_tracing.h for uprobe programs */

#ifndef __BPF_TRACING_H
#define __BPF_TRACING_H

/* pt_regs structure for x86_64 */
struct pt_regs {
	unsigned long r15;
	unsigned long r14;
	unsigned long r13;
	unsigned long r12;
	unsigned long rbp;
	unsigned long rbx;
	unsigned long r11;
	unsigned long r10;
	unsigned long r9;
	unsigned long r8;
	unsigned long rax;
	unsigned long rcx;
	unsigned long rdx;
	unsigned long rsi;
	unsigned long rdi;
	unsigned long orig_rax;
	unsigned long rip;
	unsigned long cs;
	unsigned long eflags;
	unsigned long rsp;
	unsigned long ss;
};

#endif /* __BPF_TRACING_H */
