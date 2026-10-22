#!/usr/bin/env python3
"""Geometry for the repose mark (DECISIONS I-779): two lobes joined by a
cleavage furrow. Run it to print the two paths Logo.svelte ships.

    python3 ops/dev/logo-mitosis.py [R1 R2 d r theta]

R1/R2 are the lobe radii, d is half the distance between their centres, r
is the furrow radius and theta the axis angle. The furrow is what makes
the outline pinch inward; two overlapping circles give a figure of eight.
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


if __name__ == '__main__':
    import sys
    a = sys.argv[1:]
    R1, R2, d, r, th = (float(x) for x in a) if len(a) == 5 else (23, 21, 27, 13, 45)
    g0 = geom(R1, R2, d, r, th)
    x0, y0, x1, y1 = g0['bbox']
    g = geom(R1, R2, d, r, th, 50 + (50 - (x0 + x1) / 2), 50 + (50 - (y0 + y1) / 2))
    print('mother  ', mother(g, R1, r))
    print('daughter', daughter(g, R2, r))
    print('neck %.1f  box %.1f %.1f %.1f %.1f' % (g['neck'], *g['bbox']))
