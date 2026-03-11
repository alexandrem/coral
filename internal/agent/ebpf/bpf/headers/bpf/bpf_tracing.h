/* SPDX-License-Identifier: (LGPL-2.1 OR BSD-2-Clause) */
/* Architecture-aware bpf_tracing.h for uprobe programs */

#ifndef __BPF_TRACING_H
#define __BPF_TRACING_H

#if defined(__TARGET_ARCH_arm64) || defined(__aarch64__)

/* pt_regs structure for ARM64 (AArch64).
 * Matches arch/arm64/include/asm/ptrace.h from Linux kernel.
 */
struct pt_regs {
	unsigned long long regs[31];  /* x0 – x30 */
	unsigned long long sp;
	unsigned long long pc;
	unsigned long long pstate;
};

#define PT_REGS_SP(x)  ((x)->sp)
#define PT_REGS_IP(x)  ((x)->pc)
#define PT_REGS_RC(x)  ((x)->regs[0])
#define PT_REGS_PARM1(x) ((x)->regs[0])
#define PT_REGS_PARM2(x) ((x)->regs[1])
#define PT_REGS_PARM3(x) ((x)->regs[2])
#define PT_REGS_PARM4(x) ((x)->regs[3])
#define PT_REGS_PARM5(x) ((x)->regs[4])

#else /* x86-64 */

/* pt_regs structure for x86_64. */
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

#define PT_REGS_SP(x)  ((x)->rsp)
#define PT_REGS_IP(x)  ((x)->rip)
#define PT_REGS_RC(x)  ((x)->rax)
#define PT_REGS_PARM1(x) ((x)->rdi)
#define PT_REGS_PARM2(x) ((x)->rsi)
#define PT_REGS_PARM3(x) ((x)->rdx)
#define PT_REGS_PARM4(x) ((x)->rcx)
#define PT_REGS_PARM5(x) ((x)->r8)

#endif /* architecture */

#endif /* __BPF_TRACING_H */
