// Pure model for Fig 3, from docs/proposals/2026-10-11-pause-idle-machines.md, section 9.
// One AX162-2 host: 30 seats of 8 GiB. With pause, one 8 GiB wake reserve is held back,
// so 232 GiB carry guests. A resting large guest keeps a 1.5 GiB floor in RAM and puts
// about 7 GiB in swap. f = 1 / (1 - p * (1 - floor / 8)); subscriptions = floor(29 * f).
export const SEATS = 30;
export const SEAT_GIB = 8;
export const BUDGET_GIB = 232;
export const RESERVE_GIB = 8;
export const FLOOR_GIB = 1.5;
export const SWAP_PER_REST_GIB = 7;
export const RENT_USD = 992.1;
export const SOLO_NET_USD = 27.05;
export const PLUS_NET_USD = 27.78;
export const PRO_NET_USD = 23.39;
export const SWAP_PARTITION_GIB = 160;

export const STOPS = [
  { p: 0, name: "today" },
  { p: 0.29, name: "active account" },
  { p: 0.477, name: "fleet bound" },
];

export function subscriptionsAt(p) {
  if (p <= 0) return SEATS;
  const f = 1 / (1 - p * (1 - FLOOR_GIB / SEAT_GIB));
  return Math.floor((SEATS - 1) * f);
}

export function restingAt(n, p) {
  if (p <= 0) return 0;
  return Math.max(0, Math.ceil((SEAT_GIB * n - BUDGET_GIB) / (SEAT_GIB - FLOOR_GIB)));
}

export function rentPerSeat(n) {
  return RENT_USD / n;
}

// Which guests rest first: a fixed scatter so resting machines sit among running ones.
export function restOrder(count) {
  const order = [];
  for (let i = 0; i < count; i++) order.push(i);
  let seed = 7;
  const rand = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  for (let i = order.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [order[i], order[j]] = [order[j], order[i]];
  }
  return order;
}
