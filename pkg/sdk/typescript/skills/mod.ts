/**
 * Built-in investigation skills for the Coral SDK (RFD 093).
 *
 * Skills are pre-written TypeScript functions with a defined SkillResult
 * output contract. Import them directly for use in coral_run scripts.
 *
 * @example
 * ```typescript
 * import { latencyReport } from "@coral/sdk/skills/latency-report";
 * const result = await latencyReport({ threshold_ms: 500 });
 * console.log(JSON.stringify(result));
 * ```
 *
 * @module
 */

export { latencyReport } from "./latency-report.ts";
export { errorCorrelation } from "./error-correlation.ts";
export { memoryLeakDetector } from "./memory-leak-detector.ts";
