const listeners = new Set()

export const onSubscriptionsChanged = listener => {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export const notifySubscriptionsChanged = () => {
  for (const listener of [...listeners]) listener()
}
