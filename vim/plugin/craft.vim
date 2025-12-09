" Craft code review - plugin commands and mappings
" Part of the craft vim plugin

if exists('g:loaded_craft')
  finish
endif
let g:loaded_craft = 1

" Commands
command! -nargs=? Cbase call craft#SetBase(<q-args>)
command! Cdifftool call craft#Difftool()
command! Cdiffsplit call craft#Diffsplit()

" Mappings (users can override in their vimrc)
if !exists('g:craft_no_mappings')
  nnoremap <leader>C :call craft#Comment()<CR>
  vnoremap <leader>C :call craft#Comment()<CR>
  nnoremap <leader>D :Cdiffsplit<CR>
endif
