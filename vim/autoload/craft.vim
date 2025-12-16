" Craft code review - autoload functions
" Part of the craft vim plugin

" Box drawing characters for craft comments
let s:box_thread = '╓'  " Start of new thread (header)
let s:box_reply = '╟'   " Reply within thread (header)
let s:box_body = '║'    " Body line

" Base commit for diffs (synced with gitgutter)
let g:craft_base = ''

" ============================================================================
" Comment creation
" ============================================================================

" Extract comment prefix from &commentstring (e.g., '// %s' -> '//')
function! craft#Prefix()
  let l:cs = &commentstring
  let l:idx = stridx(l:cs, '%s')
  if l:idx > 0
    return trim(l:cs[:l:idx-1])
  endif
  return '//'
endfunction

" Check if a line is a craft comment (contains any box char)
function! craft#IsCraftLine(lnum)
  let l:line = getline(a:lnum)
  return l:line =~# '[╓╟║]'
endfunction

" Set up buffer-local comments setting for craft line wrapping.
" This makes vim's formatoptions 'c' continue craft comments properly.
function! craft#SetupComments()
  " Check if already set up (idempotent)
  if &l:comments =~# '║'
    return
  endif

  " Parse existing comments setting
  let l:parts = split(&comments, ',')
  let l:new_parts = []

  for l:part in l:parts
    let l:colon = stridx(l:part, ':')
    if l:colon < 0
      continue
    endif
    let l:flags = l:colon > 0 ? l:part[:l:colon-1] : ''
    let l:chars = l:part[l:colon+1:]

    " Skip entries with f, s, m, or e flags (middle/end markers)
    if l:flags =~# '[fsme]'
      continue
    endif

    " Create craft version: original chars + ' ║' for body continuation
    call add(l:new_parts, l:flags . ':' . l:chars . ' ' . s:box_body)
  endfor

  " Prepend to comments (buffer-local)
  if len(l:new_parts) > 0
    let &l:comments = join(l:new_parts, ',') . ',' . &comments
  endif

  " These make writing comments much easier:
  setlocal formatoptions+=crqj
endfunction

" Find the end of a craft comment chain (last consecutive craft line)
function! craft#IsChainEnd(lnum)
  let l:end = a:lnum
  while craft#IsCraftLine(l:end + 1)
    let l:end += 1
  endwhile
  return l:end
endfunction

" Get the indentation (leading whitespace) of a line
function! craft#GetIndent(lnum)
  let l:line = getline(a:lnum)
  return matchstr(l:line, '^\s*')
endfunction

" Main function: reply if in chain, otherwise new comment
function! craft#Comment() range
  " PR-STATE.txt has a simpler format: no box chars, no threading
  if expand('%:t') ==# 'PR-STATE.txt'
    call craft#PRStateComment()
    return
  endif

  let l:prefix = craft#Prefix()

  " Check if we're in a craft comment chain
  if craft#IsCraftLine(line('.'))
    " Reply: go to end of chain and add new comment with reply marker
    let l:insert_after = craft#IsChainEnd(line('.'))
    let l:indent = craft#GetIndent(line('.'))
    let l:header = l:indent . l:prefix . ' ' . s:box_reply . '───── new'
  elseif a:firstline != a:lastline
    " Visual range: add range comment after last line of selection
    let l:insert_after = a:lastline
    let l:indent = craft#GetIndent(a:lastline)
    let l:range_size = a:firstline - a:lastline
    let l:header = l:indent . l:prefix . ' ' . s:box_thread . '───── new ─ range ' . l:range_size
  else
    " New comment on current line
    let l:insert_after = line('.')
    let l:indent = craft#GetIndent(line('.'))
    let l:header = l:indent . l:prefix . ' ' . s:box_thread . '───── new'
  endif

  let l:body = l:indent . l:prefix . ' ' . s:box_body . ' '
  call append(l:insert_after, [l:header, l:body])
  call cursor(l:insert_after + 2, len(l:body) + 1)
  call craft#SetupComments()
  startinsert!
endfunction

" Create a suggestion comment with the current line or visual selection
" in a ```suggestion block, ready for editing
function! craft#Suggestion() range
  let l:prefix = craft#Prefix()
  let l:indent = craft#GetIndent(a:firstline)

  " Get the selected lines
  let l:lines = getline(a:firstline, a:lastline)

  " Build the comment
  let l:result = []
  if a:firstline != a:lastline
    let l:range_size = a:firstline - a:lastline
    call add(l:result, l:indent . l:prefix . ' ' . s:box_thread . '───── new ─ range ' . l:range_size)
  else
    call add(l:result, l:indent . l:prefix . ' ' . s:box_thread . '───── new')
  endif
  call add(l:result, l:indent . l:prefix . ' ' . s:box_body . ' ```suggestion')

  " Add the copied lines (preserving their exact content)
  let l:first_content_line = len(l:result) + 1
  for l:line in l:lines
    call add(l:result, l:indent . l:prefix . ' ' . s:box_body . ' ' . l:line)
  endfor

  call add(l:result, l:indent . l:prefix . ' ' . s:box_body . ' ```')

  " Insert after the last line of the selection
  call append(a:lastline, l:result)

  " Position cursor at start of first copied line content
  let l:cursor_line = a:lastline + l:first_content_line
  let l:body_prefix = l:indent . l:prefix . ' ' . s:box_body . ' '
  call cursor(l:cursor_line, len(l:body_prefix) + 1)
  call craft#SetupComments()
endfunction

" Simpler comment creation for PR-STATE.txt
" No box chars, no threading - just top-level comments with plain text body
function! craft#PRStateComment()
  let l:insert_after = line('.')
  let l:header = '───── new'
  let l:body = ''
  call append(l:insert_after, [l:header, l:body])
  call cursor(l:insert_after + 2, 1)
  startinsert
endfunction

" ============================================================================
" Base commit management for code review
" ============================================================================

" Set the base commit for diffs
" If no argument given, gets it from 'craft base'
function! craft#SetBase(base)
  if a:base != ''
    let g:craft_base = a:base
  else
    let l:base = trim(system('craft base'))
    if v:shell_error != 0
      echohl ErrorMsg
      echo 'craft base failed: ' . l:base
      echohl None
      return 0
    endif
    let g:craft_base = l:base
  endif

  " Sync with gitgutter
  let g:gitgutter_diff_base = g:craft_base
  if exists(':GitGutterAll')
    GitGutterAll
  endif

  echo 'Base: ' . g:craft_base
  return 1
endfunction

" Ensure base is set, auto-initialize if needed
" Returns 1 if base is available, 0 if failed
function! craft#EnsureBase()
  if g:craft_base != ''
    return 1
  endif
  return craft#SetBase('')
endfunction

" Run difftool against base
function! craft#Difftool()
  if !craft#EnsureBase()
    return
  endif
  execute 'G difftool ' . g:craft_base
endfunction

" Run diffsplit against base
function! craft#Diffsplit()
  if !craft#EnsureBase()
    return
  endif
  execute 'Gdiffsplit ' . g:craft_base
endfunction
