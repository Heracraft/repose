#!/usr/bin/env python3
"""Geometry for the repose mark (DECISIONS I-779): two lobes joined by a
cleavage furrow. Run it to print the two paths Logo.svelte ships.

    python3 ops/dev/logo-mitosis.py [R1 R2 d r theta d0]

R1/R2 are the lobe radii, d is half the distance between their centres, r
is the furrow radius, theta the axis angle and d0 the half-distance of the
one-body stage the division plays from. The furrow is what makes the
outline pinch inward; two overlapping circles give a figure of eight. It
prints every number Logo.svelte and favicon.svg ship: the viewBox, the
resting paths and the stage the keyframes morph from, all centred on the
resting drawing so the lobes travel apart instead of the mark sliding.
Standard library only.
"""
import math

def geom(R1, R2, d, r, theta_deg, cx=50, cy=50):
    x1, x2 = -d, d
    fx = ((((R1+r)**2 - (R2+r)**2)/(x2-x1)) + x1 + x2)/2
    fy = math.sqrt((R1+r)**2 - (fx-x1)**2)
    th = math.radians(theta_deg); co, si = math.cos(th), math.sin(th)
    P = lambda x, y: (cx + x*co - y*si, cy + x*si + y*co)
    def tang(C, R, F):
        vx, vy = F[0]-C[0], F[1]-C[1]; L = math.hypot(vx, vy)
        return (C[0] + R*vx/L, C[1] + R*vy/L)
    C1, C2 = (x1, 0.0), (x2, 0.0)
    g = {'neck': 2*(fy - r)}
    for sgn, k in ((+1, 'a'), (-1, 'b')):
        F = (fx, sgn*fy)
        g['T1'+k] = P(*tang(C1, R1, F)); g['T2'+k] = P(*tang(C2, R2, F))
        g['N'+k] = P(fx, sgn*(fy - r))
    # bounding box of the union of the two lobes
    c1p, c2p = P(*C1), P(*C2)
    g['bbox'] = (min(c1p[0]-R1, c2p[0]-R2), min(c1p[1]-R1, c2p[1]-R2),
                 max(c1p[0]+R1, c2p[0]+R2), max(c1p[1]+R1, c2p[1]+R2))
    return g

f = lambda p: f"{p[0]:.1f} {p[1]:.1f}"

def mother(g, R1, r):
    return (f"M{f(g['T1a'])}A{R1} {R1} 0 1 1 {f(g['T1b'])}"
            f"A{r} {r} 0 0 0 {f(g['Nb'])}L{f(g['Na'])}"
            f"A{r} {r} 0 0 0 {f(g['T1a'])}Z")

def daughter(g, R2, r):
    return (f"M{f(g['T2b'])}A{R2} {R2} 0 1 1 {f(g['T2a'])}"
            f"A{r} {r} 0 0 0 {f(g['Na'])}L{f(g['Nb'])}"
            f"A{r} {r} 0 0 0 {f(g['T2b'])}Z")


def centred(R1, R2, d, r, th):
    """the geometry with its bounding box centred on the 100 grid"""
    x0, y0, x1, y1 = geom(R1, R2, d, r, th)['bbox']
    return geom(R1, R2, d, r, th, 50 + (50 - (x0 + x1) / 2), 50 + (50 - (y0 + y1) / 2))


if __name__ == '__main__':
    import sys
    a = sys.argv[1:]
    R1, R2, d, r, th, d0 = (float(x) for x in a) if len(a) == 6 else (23, 21, 26, 5.6, 45, 16)
    g, g0 = centred(R1, R2, d, r, th), centred(R1, R2, d0, r, th)
    x0, y0, x1, y1 = g['bbox']
    pad = 0.4
    print('viewBox  %.0f %.0f %.0f %.0f' % (x0 - pad, y0 - pad, x1 - x0 + 2 * pad, y1 - y0 + 2 * pad))
    print('mother        ', mother(g, R1, r))
    print('daughter      ', daughter(g, R2, r))
    print('from-mother   ', mother(g0, R1, r))
    print('from-daughter ', daughter(g0, R2, r))
    print('neck %.1f resting, %.1f at the stage the division plays from' % (g['neck'], g0['neck']))
