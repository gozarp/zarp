package nesting

// abortIndex is a cursor value larger than any legal handler chain, so a
// Context whose index reaches it runs no further handlers at any level of the
// stack. Next and Abort arrive in build order step 4; Copy already relies on it
// to mark a detached Context as spent.
const abortIndex int8 = 63
