export async function apiRequest<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  const body = await response.text();
  let payload: unknown = undefined;
  if (body.length > 0) {
    try {
      payload = JSON.parse(body);
    } catch {
      if (!response.ok) {
        throw new Error(`请求失败 (HTTP ${response.status})`);
      }
      throw new Error(`响应解析失败 (HTTP ${response.status})`);
    }
  }
  if (!response.ok) {
    const message =
      payload && typeof payload === "object" && "message" in payload && typeof (payload as { message: unknown }).message === "string"
        ? (payload as { message: string }).message
        : `请求失败 (HTTP ${response.status})`;
    throw new Error(message);
  }
  return payload as T;
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}
