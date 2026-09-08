export async function withResolverDeadline<T>(
  query: (deadline: Promise<never>) => Promise<T>,
): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const deadline = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error("deadline exceeded")), 30_000);
  });
  // Observe expiry even if startup fails before the query begins racing it.
  void deadline.catch(() => {});
  try {
    return await query(deadline);
  } finally {
    clearTimeout(timer);
  }
}
