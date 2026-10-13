// Shared resend countdown for the verification-code triggers: once a
// code has been sent — or the api answers that one was just sent — the
// trigger waits out the api's cooldown window instead of hammering the
// endpoint. The trigger is a client-side click, so the timer is the
// browser's and holds a plain handle.
export function useCooldown(seconds = 60) {
  const remaining = ref(0)
  let timer: number | undefined

  function start(): void {
    remaining.value = seconds
    if (timer !== undefined) {
      return // an interval is already ticking; just restart the count
    }
    timer = window.setInterval(() => {
      remaining.value -= 1
      if (remaining.value <= 0) {
        clearInterval(timer)
        timer = undefined
      }
    }, 1000)
  }

  onUnmounted(() => clearInterval(timer))

  return { remaining, start }
}
