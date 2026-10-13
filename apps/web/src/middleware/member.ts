// Explicit session gate (spec #24): a page that requires a Member
// session declares `definePageMeta({ middleware: 'member' })`. Without a
// session the navigation redirects to the login page carrying the
// destination as ?redirect=… so the login flow can return the Member to
// where they were going. This is the deliberate replacement for the
// legacy x-auth-token interceptor that force-redirected every 4000/3000
// response — there is no global redirect magic anywhere else.
export default defineNuxtRouteMiddleware((to) => {
  const { loggedIn } = useSession()
  if (!loggedIn.value) {
    return navigateTo({ path: '/login', query: { redirect: to.fullPath } })
  }
})
