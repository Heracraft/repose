# repose landing council rubric

Seats grade each screen from its screenshots (390 and 1440, light and dark)
1 to 10, one decimal. A screen's score is the mean of the criteria. Score
what you see now; earlier rounds do not matter. 9 and 10 are for screens a
senior designer at Linear, Vercel or Stripe would ship unchanged, and that
a developer who runs Claude Code daily would understand in three seconds
and want to try.

| Criterion | 10 looks like | 5 looks like |
|---|---|---|
| Instant value | The point lands from the figure before reading the copy | The visitor must read paragraphs or decode labels |
| Show, don't tell | A figure carries the idea; copy is a title and one line | Lists, captions and sentences explain what the picture should show |
| Economy and no repetition | Every element changes a buyer's mind; each fact once | Filler ("No token markup"), specs nobody weighs, the same fact twice |
| Visual craft | Subtle, elegant, editorial; clear focal point; calm spacing | Boxes in boxes, many borders and labels, cramped or empty regions |
| Colour and theming | Brand palette only, one accent, dark as polished as light | Off-brand colours, green or pink pills, dark slabs in light mode, gradients |
| Typography | Clear scale, serif titles, mono only for code | Too many sizes, mono for prose, weak hierarchy |
| Copy voice | Plain developer English, no AI tells, no hand-holding | Buzzwords, x-not-y, triplets, em dashes, "own hardware" |
| Owner directives | Every A5 directive visibly met | Directives missing or half done |

Hard caps:
- Horizontal overflow, clipped or overlapping text, or unreadable contrast
  caps that screen at 6.
- A claim that contradicts the facts in direction.md A6 caps it at 5.
- A rejected item from direction.md A4 on screen caps it at 6.

Findings name the screen and width, the element, what is wrong and the
exact fix, with a severity: high (caps the score, wrong fact, rejected
item), medium (costs a point), low (polish). Anything outside `/` goes in
outOfScope.
