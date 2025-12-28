/**
 * Internal gRPC client for Colony API.
 *
 * Uses Connect RPC protocol (HTTP/JSON compatible).
 *
 * @module
 * @internal
 */

import type { ClientConfig } from "./types.ts";

/**
 * Internal client for making gRPC requests to Colony.
 */
export class ColonyClient {
  private readonly baseURL: string;
  private readonly timeout: number;

  constructor(config: ClientConfig = {}) {
    // Get colony address from config or environment
    const colonyAddr = config.colonyAddr ||
      Deno.env.get("CORAL_COLONY_ADDR") ||
      "localhost:9090";

    // Construct base URL (Connect RPC uses HTTP)
    this.baseURL = `http://${colonyAddr}`;
    this.timeout = config.timeout || 30000;
  }

  /**
   * Make a gRPC request to Colony using Connect RPC protocol.
   *
   * @param service - Service name (e.g., "coral.colony.v1.ColonyService")
   * @param method - Method name (e.g., "ListServices")
   * @param request - Request payload
   * @returns Response payload
   */
  async call<TRequest, TResponse>(
    service: string,
    method: string,
    request: TRequest,
  ): Promise<TResponse> {
    const url = `${this.baseURL}/${service}/${method}`;

    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), this.timeout);

    try {
      const response = await fetch(url, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(request),
        signal: controller.signal,
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(
          `Colony API error (${response.status}): ${errorText}`,
        );
      }

      const data = await response.json();
      return data as TResponse;
    } catch (error) {
      if (error instanceof Error && error.name === "AbortError") {
        throw new Error(`Request timeout after ${this.timeout}ms`);
      }
      throw error;
    } finally {
      clearTimeout(timeoutId);
    }
  }
}

/**
 * Global client instance.
 * Created lazily on first use.
 */
let globalClient: ColonyClient | null = null;

/**
 * Get or create the global Colony client.
 *
 * @param config - Optional client configuration
 * @returns Colony client instance
 */
export function getClient(config?: ClientConfig): ColonyClient {
  if (!globalClient) {
    globalClient = new ColonyClient(config);
  }
  return globalClient;
}
